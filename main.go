package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schollz/progressbar/v3"
)

var ch = make(chan os.Signal, 1)

func main() {
	ipAddr := ""
	if len(os.Args) < 3 {
		if len(os.Args) == 2 {
			f, err := os.Open(os.Args[1])
			if err != nil {
				printUsage()
				os.Exit(1)
			}
			f.Close()
			ipAddr = discoverPrinter()
			if ipAddr == "" {
				fmt.Println("No printer found on the network.")
				os.Exit(1)
			}
		} else {
			printUsage()
			os.Exit(1)
		}
	} else {
		ipAddr = os.Args[2]
	}

	fmt.Printf("Connecting to \"%s\"...\n", (ipAddr + ":8899"))
	conn, err := net.Dial("tcp", (ipAddr + ":8899"))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Connected to printer!")
	prntOut := make(chan string, 100)
	cwrite := make(chan []byte, 100)
	m := sync.Mutex{}
	go reader(conn, prntOut)
	go writer(conn, &m, cwrite)

	fmt.Println("Establishing connection to printer...")
	cwrite <- []byte("~M601 S1\n") // Authenticate???
	str := <-prntOut
	if !strings.Contains(str, "Control Success.") {
		conn.Close()
		os.Exit(1)
	}
	fmt.Println("Connection established!")
	go writeFile(prntOut, cwrite)

	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch

	cwrite <- []byte("~M602\n")
	fmt.Println("Disconnected from printer.")
	conn.Close()
}

func writeFile(prntOut chan string, cwrite chan []byte) {
	file, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println(err)
		ch <- os.Interrupt
	}
	data := getchunks(file, 4096)
	cwrite <- []byte(fmt.Sprintf("~M28 %d 0:/user/%s\n", len(file), filepath.Base(os.Args[1])))
	str := <-prntOut
	if !strings.Contains(str, "Writing to file") {
		fmt.Println("Unexpected response from printer.")
		ch <- os.Interrupt
	}
	bar := progressbar.NewOptions(len(file),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(15),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetDescription("Sending data to printer..."),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
	for i := 0; i < len(data); i++ {
		chunk := data[i]
		fc := getcrc(chunk)
		sl := make([]byte, 4)
		binary.LittleEndian.PutUint32(sl, uint32(len(chunk)))
		if len(chunk) < 4096 {
			for {
				if len(chunk) == 4096 {
					break
				}
				chunk = append(chunk, 0x00)
			}
		}

		sn := make([]byte, 4)
		binary.LittleEndian.PutUint32(sn, uint32(i))

		header := []byte{0x5A, 0x5A, 0xA5, 0xA5, sn[3], sn[2], sn[1], sn[0], sl[3], sl[2], sl[1], sl[0], fc[3], fc[2], fc[1], fc[0]}
		sending := append(header, chunk[:]...)

		cwrite <- sending
		str := <-prntOut

		if !strings.Contains(str, fmt.Sprintf("%d ok.", i)) {
			bar.Close()
			switch {
			case strings.Contains(str, "error"):
				fmt.Println("There was an error sending data to the printer, this should rarely happen.")
				fmt.Println("If this happens regularly, or with a specific .gcode file, please make a github issue.")
				ch <- os.Interrupt
			default:
				fmt.Println("Unexpected response from printer after sending slice: ", str)
				fmt.Println("If this happens, please make a github issue.")
				ch <- os.Interrupt
			}
		}
		bar.Add(4096)
	}
	bar.Finish()

	cwrite <- []byte("~M29\n")
	str = <-prntOut
	if !strings.Contains(str, "Done saving file") {
		bar.Close()
		fmt.Println("Unexpected response from printer after M29:", str)
		fmt.Println("If this happens, please make a github issue.")
		ch <- os.Interrupt
	}
	fmt.Println("\nFile saved.")

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Would you like to start printing this file? Y/N: ")
	text, _ := reader.ReadString('\n')
	if strings.Contains(strings.ToLower(text), "y") {
		cwrite <- []byte("~M23 0:/user/" + filepath.Base(os.Args[1]) + "\n")
		str = <-prntOut
		if !strings.Contains(str, "File opened") {
			fmt.Println("Unexpected response from printer after M23:", str)
			fmt.Println("If this happens, please make a github issue.")
			ch <- os.Interrupt
		}
		fmt.Println("Printing...")
	}

	ch <- os.Interrupt
}

func discoverPrinter() string {
	fmt.Println("Searching for printer on the network...")
	type printer struct {
		ip   string
		name string
	}

	printers := make([]printer, 0)
	timeout := 0

	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		fmt.Println("Error dialing UDP:", err)
		return ""
	}

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	preferredIp := localAddr.IP
	conn.Close()

	addr, err := net.ResolveUDPAddr("udp", preferredIp.String()+":18002")
	if err != nil {
		fmt.Println("Error resolving UDP address:", err)
		return ""
	}

	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Println("Error listening on UDP:", err)
		return ""
	}

	go func() {
		for {
			buf := make([]byte, 1024)
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				fmt.Println("Error reading UDP:", err)
				return
			}
			if len(buf[:n]) > 128 {
				newPrinter := printer{ip: addr.IP.String(), name: strings.TrimRight(string(buf[:128]), "\x00")}
				// loop through printers to see if newPrinter is already in the list
				found := false
				for i := 0; i < len(printers); i++ {
					if printers[i].ip == newPrinter.ip {
						found = true
						break
					}
				}
				if !found {
					printers = append(printers, newPrinter)
				}
			}
		}
	}()

	ipBytes := preferredIp.To4()
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(18002))

	discoveryPayload := append(ipBytes, append(portBytes, []byte{0x00, 0x00}...)...)

	for {
		if timeout >= 5 {
			break
		}

		udpConn.WriteToUDP(discoveryPayload, &net.UDPAddr{IP: net.IPv4(225, 0, 0, 9), Port: 19000})

		timeout++
		time.Sleep(1 * time.Second)

		printerList := ""
		for i := 0; i < len(printers); i++ {
			printerList = printerList + fmt.Sprintf("%d: %s@%s, ", i, printers[i].name, printers[i].ip)
		}
		if printerList != "" {
			printerList = printerList[:len(printerList)-2]
		}

		fmt.Printf("\033[2K\rPrinters discovered: %d - %s", len(printers), printerList)

	}
	fmt.Println()
	if len(printers) == 0 {
		return ""
	}
	index := 0
	for {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Please select desired printer via index: ")
		text, _ := reader.ReadString('\n')
		indexStr := strings.TrimSpace(text)
		indexNum, err := strconv.Atoi(indexStr)
		if err != nil {
			fmt.Println("Invalid input.")
			continue
		}
		if indexNum < 0 || indexNum >= len(printers) {
			fmt.Println("Invalid input.")
			continue
		}
		index = indexNum
		break
	}

	return printers[index].ip
}

func getchunks(data []byte, clength int) [][]byte {
	var sliceofslices [][]byte
	var inc = 0
	for i := 0; i < len(data); i++ {
		fbytes := make([]byte, 0)      // One chunk of 4096 bytes
		for j, n := range data[inc:] { //haha i feel very cool about this
			if j >= clength {
				break
			}
			fbytes = append(fbytes, n)
		}

		sliceofslices = append(sliceofslices, []byte{})
		sliceofslices[i] = fbytes
		if (inc + clength) > len(data) {
			break
		} else {
			inc = inc + clength
		}
	}

	return sliceofslices
}

func printUsage() {
	execName, err := os.Executable()
	if err != nil {
		execName = "voxelab-comms"
	}
	fmt.Printf("Usage: %s ./path/to/file.gcode {Printer IP}(optional)\n", execName)
	fmt.Println("Example: voxelab-comms ./test.gcode")
	fmt.Println("	In this case, the printer will be searched for on the local network.")
	fmt.Println("Example: voxelab-comms C:/gcode/test.gcode 192.168.0.136")
	fmt.Println("	In this case, communication will be attempted to 192.168.0.136:8899")
	os.Exit(1)
}

func getcrc(dat []byte) []byte { // god
	soup := crc32.ChecksumIEEE(dat)
	r := make([]byte, 4)
	for i := uint32(0); i < 4; i++ {
		r[i] = byte((soup >> (8 * i)) & 0xff)
	}
	return r
}

func writer(conn net.Conn, m *sync.Mutex, cwrite chan []byte) {
	for {
		str := <-cwrite
		m.Lock()
		conn.Write(str)
		m.Unlock()
	}
}

func reader(conn net.Conn, prntOut chan string) {
	connbuf := bufio.NewReader(conn)
	for {
		resp := ""
		for {
			respTemp, _ := connbuf.ReadString('\n')
			resp = resp + respTemp
			if strings.HasSuffix(resp, "ok.\r\n") || strings.HasSuffix(resp, "ok\r\n") {
				break
			}
		}
		if len(resp) > 0 {
			prntOut <- string(resp)
		}
	}
}
