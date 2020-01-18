// Package telnet implements base Telnet protocol specification.
// It is primarily intended for implementing Telnet clients.
// Maybe it can be helpful in Telnet server implementations too (if someone dare to make one)
package telnet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
)

const bufferSize int = 1024

// CommandCode type represents Telnet command codes
type CommandCode byte

// Telnet commands as per https://tools.ietf.org/html/rfc854
const (
	SE   CommandCode = 240
	SB   CommandCode = 250
	WILL CommandCode = 251
	WONT CommandCode = 252
	DO   CommandCode = 253
	DONT CommandCode = 254
	IAC  CommandCode = 255
)

// OptionCode type represents Telnet option codes
type OptionCode byte

// Telnet option codes as per https://www.iana.org/assignments/telnet-options/telnet-options.xhtml
const (
	BinaryTransmission       OptionCode = 0
	Echo                     OptionCode = 1
	SuppressGoAhead          OptionCode = 3
	Status                   OptionCode = 5
	TerminalType             OptionCode = 24
	NegotiateAboutWindowSize OptionCode = 31
	TerminalSpeed            OptionCode = 32
	RemoteFlowControl        OptionCode = 33
)

type optionHandler func(w *Connection, data []byte) error

type option struct {
	weWill    bool                // true if we WILL perform option, false if we WON'T
	peerDo    bool                // true if we DO allow peer to preform option, false if we DONT
	sbHandler optionHandler
	doHandler optionHandler
}

// Operation is not the term from Telnet specification.
// It is defined and used in this package for convenience and clarity
// Operations are used in Telnet subcommands (like TERMINAL-TYPE IS, TERMINAL-TYPE SEND in TERMINAL-TYPE (24) command)
type Operation byte

// Telnet operations are option-specific. SEND and IS are common ones
const (
	NONE Operation = 255
	IS   Operation = 0
	SEND Operation = 1
)

type streamState int

const (
	stateData streamState = iota
	stateInIAC
	stateInSB
	stateEscIAC
	stateNotReady
)

// Connection provides standard ReadWriter interface to communicate with remote site and
// transparently handles any Telnet commands via registered OptionHandlers
type Connection struct {
	conn       net.Conn
	options    map[OptionCode]*option
	state      streamState
	winSize    []byte
}

// Connect returns Connection ready to serve Telnet data stream
func Connect(address string) (*Connection, error) {
	var connection Connection
	var err error

	connection.state = stateNotReady
	connection.conn, err = net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	connection.AddOption(BinaryTransmission, false, false, nil, nil)
	connection.AddOption(Echo, false, true, nil, nil)
	connection.AddOption(SuppressGoAhead, true, true, nil, nil)
	connection.AddOption(Status, false, false, nil, nil)
	connection.AddOption(TerminalType, true, true, terminalTypeOptionHandler, nil)
	connection.AddOption(NegotiateAboutWindowSize, true, true, nil, negotiateAboutWindowSizeOptionHandler)
	connection.AddOption(TerminalSpeed, true, true, terminalSpeedOptionHandler, nil)
	connection.AddOption(RemoteFlowControl, false, false, nil, nil)

	connection.state = stateData

	return &connection, err
}

// Read implements standard io.Reader interface
func (p Connection) Read(b []byte) (n int, err error) {
	if p.state == stateNotReady {
		return 0, errors.New("telnet connection is not ready")
	}

	var outputData bytes.Buffer
	for {
		rawData := make([]byte, bufferSize)
		n, err = p.conn.Read(rawData)
		if n > 0 {
			if err := p.processCommands(bytes.NewBuffer(rawData[:n]), &outputData); err != nil {
				n, _ = outputData.Read(b)
				return n, err
			}
			n, _ = outputData.Read(b)
			if n > 0 {
				return n, nil
			}
		}
		if err != nil {
			return n, err
		}
	}
}

// Write implements standard io.Writer interface
func (p Connection) Write(b []byte) (n int, err error) {
	if p.state == stateNotReady {
		return 0, errors.New("telnet connection is not ready")
	}
	return p.conn.Write(b)
}

// Close implements standard io.Closer interface
func (p *Connection) Close() error {
	p.state = stateNotReady
	return p.conn.Close()
}

// AddOption adds Telnet option to handle by Connection
func (p *Connection) AddOption(optionCode OptionCode, weWill bool, peerDo bool, sbHandler optionHandler, doHandler optionHandler) {
	if p.options == nil {
		p.options = make(map[OptionCode]*option)
	}
	option := option{
		weWill:     weWill,
		peerDo:     peerDo,
		sbHandler:  sbHandler,
		doHandler:  doHandler,
	}
	p.options[optionCode] = &option
}

// DisableRemoteEcho instructs remote peer to not echo
func (p *Connection) DisableRemoteEcho() error {
	command := []byte {byte(IAC), byte(DONT), byte(Echo)}
	_, err := p.conn.Write(command)
	return err
}

// SetWindowSize updates internal window size data and sends NAWS option to remote peer
func (p *Connection) SetWindowSize(width, height int) {
	if len(p.winSize) != 4 {
		p.winSize = make([]byte, 4)
	}
	binary.BigEndian.PutUint16(p.winSize[:2], uint16(width))
	binary.BigEndian.PutUint16(p.winSize[2:], uint16(height))
	_ = negotiateAboutWindowSizeOptionHandler(p, nil)
}

func (p *Connection) processCommands(buffer *bytes.Buffer, outputData *bytes.Buffer) error {
	commandBuffer := make([]byte, 0, bufferSize)
	for _, b := range buffer.Bytes() {
		switch p.state {
		case stateNotReady:
			return errors.New("telnet connection is not ready")
		case stateData:
			if CommandCode(b) == IAC {
				p.state = stateInIAC
				commandBuffer = append(commandBuffer, b)
			} else {
				outputData.WriteByte(b)
			}

		case stateInIAC:
			commandBuffer = append(commandBuffer, b)
			if CommandCode(b) == WILL || CommandCode(b) == WONT || CommandCode(b) == DO || CommandCode(b) == DONT {
				// Stay in this state, awaiting option code
			} else if CommandCode(b) == IAC {
				outputData.WriteByte(b)
				p.state = stateData
			} else if CommandCode(b) == SB {
				p.state = stateInSB
			} else { // option code
				if err := p.negotiateOption(commandBuffer); err != nil {
					return err
				}
				commandBuffer = commandBuffer[:0]
				p.state = stateData
			}

		case stateInSB:
			if b == byte(IAC) {
				p.state = stateEscIAC
			}
			commandBuffer = append(commandBuffer, b)

		case stateEscIAC:
			commandBuffer = append(commandBuffer, b)
			if b == byte(IAC) {
				p.state = stateInSB
			}
			if b == byte(SE) {
				if err := p.negotiateOption(commandBuffer); err != nil {
					return err
				}
				commandBuffer = commandBuffer[:0]
				p.state = stateData
			}

		}
	}
	return nil
}

func (p Connection) negotiateOption(data []byte) error {
	var receiverCommand = CommandCode(data[1])
	var receivedOptionCode = OptionCode(data[2])
	var err error

	option, optionExist := p.options[receivedOptionCode]

	switch receiverCommand {
	case DO:
		if !optionExist { // If we don't have received option in peer configuration, we WONT handle it
			data[1] = byte(WONT)
			_, err = p.conn.Write(data)
			return nil
		}
		if option.weWill {
			data[1] = byte(WILL)
		} else {
			data[1] = byte(WONT)
		}
		_, err = p.conn.Write(data)
		if option.doHandler != nil {
			err = p.options[receivedOptionCode].doHandler(&p, data)
		}

	case WILL:
		if !optionExist { // If we don't have received option in peer configuration, we DONT handle it
			data[1] = byte(DONT)
			_, err = p.conn.Write(data)
			return nil
		}
		if option.peerDo {
			data[1] = byte(DO)
		} else {
			data[1] = byte(DONT)
		}
		_, err = p.conn.Write(data)

	case SB:
		if optionExist && option.sbHandler != nil {
			err = p.options[receivedOptionCode].sbHandler(&p, data)
		}
		// TODO What we should do if we got unhandled SB?
	}

	return err
}

func (p Connection) sendSB(option OptionCode, operation Operation, data []byte) error {
	result := make([]byte, 0, bufferSize)
	result = append(result, byte(IAC), byte(SB), byte(option))
	if operation != NONE {
		result = append(result, byte(operation))
	}
	result = append(result, data...)
	result = append(result, byte(IAC), byte(SE))
	_, err := p.Write(result)
	return err
}

// https://tools.ietf.org/html/rfc1091
// IAC SB TERMINAL-TYPE SEND IAC SE
// IAC SB TERMINAL-TYPE IS ... IAC SE
func terminalTypeOptionHandler(w *Connection, data []byte) error {
	operation := Operation(data[3])
	if operation == SEND {
		termString, ok := os.LookupEnv("TERM")
		if !ok {
			// TODO send WONT?
			return nil
		}
		term := []byte(termString)
		return w.sendSB(TerminalType, IS, term)
	}
	if operation == IS {
		// Not implemented
	}
	return nil
}

// https://tools.ietf.org/html/rfc1073
// IAC SB NAWS <16-bit value> <16-bit value> IAC SE
func negotiateAboutWindowSizeOptionHandler(w *Connection, data []byte) error {
	return w.sendSB(NegotiateAboutWindowSize, NONE, w.winSize)
}

// https://tools.ietf.org/html/rfc1079
// IAC SB TERMINAL-SPEED SEND IAC SE
// IAC SB TERMINAL-SPEED IS ... IAC SE
func terminalSpeedOptionHandler(w *Connection, data []byte) error {
	operation := Operation(data[3])
	if operation == SEND {
		speed := []byte("115200,115200") // Someone will ever use this nowadays?
		return w.sendSB(TerminalSpeed, IS, speed)
	}
	if operation == IS {
		// Not implemented
	}
	return nil
}
