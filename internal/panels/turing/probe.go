package turing

import (
	"time"

	"github.com/n-kisyov/wininfopanel/internal/panels/serial"
)

// Probe opens a port, sends the handshake, and returns whatever comes back
// within window, without interpreting any of it.
//
// It exists because a panel that does not answer is otherwise indistinguishable
// from one that answers something unexpected, and the difference decides
// whether the problem is the wiring or the protocol.
func Probe(portName string, window time.Duration) ([]byte, error) {
	if window <= 0 {
		window = 3 * time.Second
	}

	port, err := serial.Open(portName, serial.Options{
		BaudRate:     115200,
		ReadTimeout:  250 * time.Millisecond,
		WriteTimeout: time.Second,
	})
	if err != nil {
		return nil, err
	}
	defer port.Close()

	if err := port.Discard(); err != nil {
		return nil, err
	}

	out := newBlockWriter(port)
	if err := out.write(cmdHello, 0x00); err != nil {
		return nil, err
	}
	if err := out.flush(0x00); err != nil {
		return nil, err
	}

	var received []byte
	buffer := make([]byte, readSize)
	deadline := time.Now().Add(window)

	for time.Now().Before(deadline) && len(received) < readSize {
		n, err := port.Read(buffer)
		if err != nil {
			return received, err
		}
		received = append(received, buffer[:n]...)
	}
	return received, nil
}
