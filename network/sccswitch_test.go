// Copyright 2025 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package network

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

func TestConfigureSCC(t *testing.T) {
	username := "username"
	password := "password"
	upCommands := []string{
		"up_line1",
		"up line 2",
		"up-line/3",
		"up line 4",
		"up line 5",
		"exit",
	}
	downCommands := []string{
		"down_line1",
		"down line 2",
		"down-line/3",
		"down line 4",
		"down line 5",
		"exit",
	}
	scc := NewSCCSwitch("127.0.0.1", username, password, upCommands, downCommands)
	scc.port = 9150
	scc.connectTimeoutDuration = 10 * time.Millisecond
	scc.configTimeoutDuration = 15 * time.Millisecond

	// Set the switch to the down state
	received := mockSSHSwitch(t, scc.port, username, password)
	assert.Nil(t, scc.SetTeamEthernetEnabled(false))
	assert.Equal(t, downCommands, receiveCommands(t, received))
	assert.Equal(t, "DISABLED", scc.Status)
	assert.Equal(t, "DISABLED", scc.GetStatus())

	// Set the switch to the up state
	scc.port += 1
	received = mockSSHSwitch(t, scc.port, username, password)
	assert.Nil(t, scc.SetTeamEthernetEnabled(true))
	assert.Equal(t, upCommands, receiveCommands(t, received))
	assert.Equal(t, "ACTIVE", scc.Status)
}

// Returns the commands the mock switch was sent.
func receiveCommands(t *testing.T, received chan []string) []string {
	select {
	case commands := <-received:
		return commands
	case <-time.After(5 * time.Second):
		assert.Fail(t, "the switch was not sent any commands")
		return nil
	}
}

// Stands in for the switch: it takes one SSH session on the port and sends the commands it was given on the channel
// it returns.
func mockSSHSwitch(t *testing.T, port int, username, password string) chan []string {
	received := make(chan []string, 1)
	go func() {
		// Create a simple SSH server that accepts a connection with password authentication
		_, privateKey, err := ed25519.GenerateKey(nil)
		assert.Nil(t, err)
		signer, err := ssh.NewSignerFromKey(privateKey)
		assert.Nil(t, err)
		config := &ssh.ServerConfig{
			PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
				assert.Equal(t, username, conn.User())
				assert.Equal(t, password, string(pass))
				return nil, nil
			},
		}
		config.AddHostKey(signer)
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if !assert.Nil(t, err) {
			return
		}
		defer listener.Close()
		nConn, err := listener.Accept()
		if !assert.Nil(t, err) {
			return
		}
		defer nConn.Close()
		conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
		assert.Nil(t, err)
		defer conn.Close()

		// Ignore all client requests
		go ssh.DiscardRequests(reqs)

		// Wait for the client to connect and request a session
		rawChannel := <-chans
		assert.Equal(t, "session", rawChannel.ChannelType())
		channel, requests, err := rawChannel.Accept()
		assert.Nil(t, err)
		defer channel.Close()

		// Wait for the client to request a PTY
		req := <-requests
		assert.Equal(t, "pty-req", req.Type)
		req.Reply(true, nil)

		// Wait for the client to request a shell
		req = <-requests
		assert.Equal(t, "shell", req.Type)
		req.Reply(true, nil)

		// Read all data sent by the client. The reader may still be at it when the wait below runs out, so what
		// it has read so far is behind a lock.
		var receivedData bytes.Buffer
		var receivedMutex sync.Mutex
		done := make(chan struct{})
		go func() {
			defer close(done)
			buffer := make([]byte, 1024)
			for {
				n, err := channel.Read(buffer)
				if err != nil {
					assert.Equal(t, io.EOF, err)
					break
				}
				receivedMutex.Lock()
				receivedData.Write(buffer[:n])
				receivedMutex.Unlock()
			}
		}()

		select {
		case <-done:
			// Client closed the channel
		case <-time.After(5 * time.Millisecond):
			// All data should be read by now. Close the connection
		}

		receivedMutex.Lock()
		commands := strings.Split(receivedData.String(), "\n")
		receivedMutex.Unlock()
		if len(commands) > 0 && commands[len(commands)-1] == "" {
			commands = commands[:len(commands)-1] // Remove trailing newline
		}
		received <- commands

		// Send an exit command to cleanly close the session
		channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
	}()
	time.Sleep(100 * time.Millisecond) // Give it some time to open the socket.
	return received
}
