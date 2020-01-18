Basic Telnet protocol implementation in GO (golang).

Telnet client example:
```go
package main

import (
	"fmt"
	"github.com/plyul/telnet"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func main() {
	address := "localhost:2023"
	connection, err := telnet.Connect(address)
	if err != nil {
		fmt.Print(err)
		return
	}
	defer connection.Close()

	fd := int(os.Stdout.Fd())
	w, h, _ := getSize(fd)
	connection.SetWindowSize(w, h)
	
	go io.Copy(connection, os.Stdin)
	_, err = io.Copy(os.Stdout, connection)
}

func getSize(fd int) (width, height int, err error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return -1, -1, err
	}
	return int(ws.Col), int(ws.Row), nil
}
```