package telnet

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"testing"
)

func startServer(t *testing.T, outputData []byte, serverReady chan bool) {
	go func() {
		listener, err := net.Listen("tcp", ":2323")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		serverReady <- true
		conn, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		rw.Write(outputData)
		rw.Flush()
		conn.Close()
	}()

}

func TestConnect(t *testing.T) {
	serverReady := make(chan bool)
	serverData := []byte("Hello telnet")
	go startServer(t, serverData, serverReady)
	<-serverReady
	telnetConn, err := Connect("localhost:2323")
	if err != nil {
		t.Fatal("Can't connect to telnet server: ", err)
	}

	data := make([]byte, 64)
	n, err := telnetConn.Read(data)
	if n != len(serverData) {
		t.Errorf("Received wrong number of bytes from server. Expected %d, got %d", len(serverData), n)
	}

	_, err = telnetConn.Read(data)
	if err != io.EOF {
		t.Error("Unexpected data from telnet connection")
	}

	err = telnetConn.Close()
	if err != nil {
		t.Error("Error closing telnet connection: ", err)
	}

	n, err = telnetConn.Read(data)
	if err == nil || n != 0 || err.Error() != "telnet connection is not ready" {
		t.Fatal("Connection must return 'not ready' error after Close: ", err)
	}
}

func TestAddOption(t *testing.T) {
	c := Connection{
		conn:    nil,
		options: nil,
		state:   0,
		winSize: nil,
	}
	cmd := OptionCode(88)
	c.AddOption(cmd, true, true, nil, nil)
	option, optionExist := c.options[cmd]
	if !optionExist || !option.weWill || !option.peerDo {
		t.Error("Failed to add option")
	}
}

func TestProcessCommands(t *testing.T) {
	c := Connection{
		conn:    nil,
		options: nil,
		state:   stateNotReady,
		winSize: nil,
	}

	var input bytes.Buffer
	var output bytes.Buffer

	inputData := []byte("plain data")
	input.Write(inputData)
	err := c.processCommands(&input, &output)
	if err.Error() != "telnet connection is not ready" {
		t.Error("Failed while processing in notReady state")
	}

	c.state = stateData
	_ = c.processCommands(&input, &output)
	if !bytes.Equal(output.Bytes(), inputData) {
		t.Error("Failed while processing plain data: ", output.Bytes())
	}

	c.state = stateData
	inputData = []byte{72, 255, 255, 116, 101, 108}
	outputData := []byte{72, 255, 116, 101, 108}
	input.Reset()
	output.Reset()
	input.Write(inputData)
	_ = c.processCommands(&input, &output)
	if !bytes.Equal(output.Bytes(), outputData) {
		t.Error("Failed while processing escaped 0x255: ", output.Bytes())
	}
}
