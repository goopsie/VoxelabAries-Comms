package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schollz/progressbar/v3"
)

const gcodepath = "C:/Users/Goopsie/go/src/voxelab-comms/prismo_v4.gcode"
const printerip = "192.168.0.104:8899"
const packetlength = 4096 // only works with 4096

func main() {
	fmt.Printf("Connecting to \"%s\"...\n", printerip)
	conn, err := net.Dial("tcp", printerip)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Connected!")
	prntOut := make(chan string, 100)
	cwrite := make(chan []byte, 100)
	m := sync.Mutex{}
	go reader(conn, prntOut)
	go writer(conn, &m, cwrite)

	cwrite <- []byte("~M601 S1\n") // Authenticate???
	str := <-prntOut
	if !strings.Contains(str, "Control Success.") {
		conn.Close()
		os.Exit(1)
	}
	go writeFile(prntOut, cwrite)

	ch := make(chan os.Signal, 1)

	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	conn.Close()
}

func writeFile(prntOut chan string, cwrite chan []byte) {
	file, err := os.ReadFile(gcodepath)
	if err != nil {
		panic(err)
	}
	data := getchunks(file, packetlength)
	cwrite <- []byte(fmt.Sprintf("~M28 %d 0:/user/prismo_v4.gcode\n", len(file)))
	str := <-prntOut
	if !strings.Contains(str, "Writing to file") {
		panic(str)
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
		if len(chunk) < packetlength {
			for {
				if len(chunk) == packetlength {
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
			fmt.Println(str)
			panic("balls")
		}
		bar.Add(packetlength)
	}

	cwrite <- []byte("~M29")
	str = <-prntOut
	if !strings.Contains(str, "Done saving file") {
		panic("balls")
	}
	fmt.Println("File saved.")
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

func reader(conn net.Conn, prntOut chan string) { // this does not feel like the right way
	connbuf := bufio.NewReader(conn)
	for {
		resp := ""
		for {
			conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
			respTemp, err := connbuf.ReadString('\n')
			if err != nil {
				break
			}
			resp = resp + respTemp
		}
		if len(resp) > 0 {
			prntOut <- string(resp)
		}
	}
}
