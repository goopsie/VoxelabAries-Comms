package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const gcodepath = "C:/Users/Goopsie/Voxelab-Comms/3DBenchy.gcode"

func main() {
	conn, err := net.Dial("tcp", "192.168.0.116:8899")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	connbuf := bufio.NewReader(conn)
	conn.Write([]byte("~M601 S1\n")) // Authenticate???
	str, _ := connbuf.ReadString('\n')
	if !strings.Contains(str, "M601 Received.") {
		str, _ = connbuf.ReadString('\n')
	} else if !strings.Contains(str, "Control Success.") {
		str, _ = connbuf.ReadString('\n')
	} else if !strings.Contains(str, "ok") {
		str, _ = connbuf.ReadString('\n')
	} else {
		fmt.Println(str)
	}
	//amongus := make(chan string, 100)
	cwrite := make(chan []byte, 100)
	m := sync.Mutex{}
	//go reader(conn, amongus)
	go writer(conn, &m, cwrite)
	go dostuff(connbuf, cwrite)
	time.Sleep(time.Second * 100)
}

func dostuff(connbuf *bufio.Reader, cwrite chan []byte) {
	file, err := os.ReadFile(gcodepath)
	if err != nil {
		panic("sus among us")
	}
	data := getchunks(file)
	cwrite <- []byte(fmt.Sprintf("~M28 %d 0:/user/3DPoopchy.gcode\n", len(file)))
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < len(data); i++ {
		chunk := data[i]
		fc := getcrc(chunk)
		fl := make([]byte, 4)
		binary.LittleEndian.PutUint32(fl, uint32(len(chunk)))
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

		header := []byte{0x5A, 0x5A, 0xA5, 0xA5, sn[3], sn[2], sn[1], sn[0], fl[3], fl[2], fl[1], fl[0], fc[3], fc[2], fc[1], fc[0]}
		sending := append(header, chunk[:]...)
		cwrite <- sending
		fmt.Println("sending it!!")
		str, _ := connbuf.ReadString('\n')
		fmt.Println(str)
		if !strings.Contains(str, fmt.Sprintf("%d ok.", i)) {
			if strings.Contains(str, "error.") {
				break
			}
			for {
				str, _ := connbuf.ReadString('\n')
				fmt.Println(str)
				fmt.Println("Checking if contains")
				if strings.Contains(str, fmt.Sprintf("%d ok.", i)) || strings.Contains(str, "error.") {
					break
				}
			}
		}
		fmt.Println(str)
	}
	time.Sleep(200 * time.Millisecond)
	cwrite <- []byte("~M29")
	fmt.Println("File should be saved")

}

func getchunks(data []byte) [][]byte {
	var sliceofslices [][]byte
	var inc = 0
	for i := 0; i < len(data); i++ {
		fbytes := make([]byte, 0)      // One chunk of 4096 bytes
		for j, n := range data[inc:] { //haha i feel very cool about this
			if j >= 4096 {
				break
			}
			fbytes = append(fbytes, n)
		}

		sliceofslices = append(sliceofslices, []byte{})
		sliceofslices[i] = fbytes
		if (inc + 4096) > len(data) {
			break
		} else {
			inc = inc + 4096
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

//func reader(conn net.Conn, amongus chan string) {
//	connbuf := bufio.NewReader(conn)
//	for {
//		str, err := connbuf.ReadString('\n')
//		if err != nil {
//			break
//		}
//
//		if len(str) > 0 {
//			amongus <- str
//		}
//	}
//}
