// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package network

import (
	"bytes"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"net"
	"testing"
	"time"
)

func TestConfigureSwitch(t *testing.T) {
	sw := NewSwitch("127.0.0.1", "password")
	assert.Equal(t, "UNKNOWN", sw.Status)
	sw.port = 9050
	sw.configBackoffDuration = time.Millisecond
	sw.configPauseDuration = time.Millisecond
	expectedResetCommand := "password\nenable\npassword\nterminal length 0\nconfig terminal\n" +
		"interface Vlan10\nno ip address\nno ip dhcp pool dhcp10\n" +
		"interface Vlan20\nno ip address\nno ip dhcp pool dhcp20\n" +
		"interface Vlan30\nno ip address\nno ip dhcp pool dhcp30\n" +
		"interface Vlan40\nno ip address\nno ip dhcp pool dhcp40\n" +
		"interface Vlan50\nno ip address\nno ip dhcp pool dhcp50\n" +
		"interface Vlan60\nno ip address\nno ip dhcp pool dhcp60\n" +
		"end\nexit\n"

	// Should remove all previous VLANs and do nothing else if current configuration is blank.
	commands := mockTelnet(t, sw.port)
	assert.Nil(t, sw.ConfigureTeamEthernet([6]*model.Team{nil, nil, nil, nil, nil, nil}))
	assert.Equal(t, expectedResetCommand, receiveCommand(t, commands))
	assertNoCommand(t, commands)
	assert.Equal(t, "ACTIVE", sw.Status)
	assert.Equal(t, "ACTIVE", sw.GetStatus())

	// Should configure one team if only one is present.
	sw.port += 1
	commands = mockTelnet(t, sw.port)
	assert.Nil(t, sw.ConfigureTeamEthernet([6]*model.Team{nil, nil, nil, nil, {Id: 254}, nil}))
	assert.Equal(t, expectedResetCommand, receiveCommand(t, commands))
	assert.Equal(
		t,
		"password\nenable\npassword\nterminal length 0\nconfig terminal\n"+
			"ip dhcp excluded-address 10.2.54.1 10.2.54.19\nip dhcp excluded-address 10.2.54.200 10.2.54.254\nip dhcp pool dhcp50\n"+
			"network 10.2.54.0 255.255.255.0\ndefault-router 10.2.54.4\nlease 7\n"+
			"interface Vlan50\nip address 10.2.54.4 255.255.255.0\n"+
			"end\nexit\n",
		receiveCommand(t, commands),
	)

	// Should configure all teams if all are present.
	sw.port += 1
	commands = mockTelnet(t, sw.port)
	assert.Nil(
		t,
		sw.ConfigureTeamEthernet([6]*model.Team{{Id: 1114}, {Id: 254}, {Id: 296}, {Id: 1503}, {Id: 1678}, {Id: 1538}}),
	)
	assert.Equal(t, expectedResetCommand, receiveCommand(t, commands))
	assert.Equal(
		t,
		"password\nenable\npassword\nterminal length 0\nconfig terminal\n"+
			"ip dhcp excluded-address 10.11.14.1 10.11.14.19\nip dhcp excluded-address 10.11.14.200 10.11.14.254\nip dhcp pool dhcp10\n"+
			"network 10.11.14.0 255.255.255.0\ndefault-router 10.11.14.4\nlease 7\n"+
			"interface Vlan10\nip address 10.11.14.4 255.255.255.0\n"+
			"ip dhcp excluded-address 10.2.54.1 10.2.54.19\nip dhcp excluded-address 10.2.54.200 10.2.54.254\nip dhcp pool dhcp20\n"+
			"network 10.2.54.0 255.255.255.0\ndefault-router 10.2.54.4\nlease 7\n"+
			"interface Vlan20\nip address 10.2.54.4 255.255.255.0\n"+
			"ip dhcp excluded-address 10.2.96.1 10.2.96.19\nip dhcp excluded-address 10.2.96.200 10.2.96.254\nip dhcp pool dhcp30\n"+
			"network 10.2.96.0 255.255.255.0\ndefault-router 10.2.96.4\nlease 7\n"+
			"interface Vlan30\nip address 10.2.96.4 255.255.255.0\n"+
			"ip dhcp excluded-address 10.15.3.1 10.15.3.19\nip dhcp excluded-address 10.15.3.200 10.15.3.254\nip dhcp pool dhcp40\n"+
			"network 10.15.3.0 255.255.255.0\ndefault-router 10.15.3.4\nlease 7\n"+
			"interface Vlan40\nip address 10.15.3.4 255.255.255.0\n"+
			"ip dhcp excluded-address 10.16.78.1 10.16.78.19\nip dhcp excluded-address 10.16.78.200 10.16.78.254\nip dhcp pool dhcp50\n"+
			"network 10.16.78.0 255.255.255.0\ndefault-router 10.16.78.4\nlease 7\n"+
			"interface Vlan50\nip address 10.16.78.4 255.255.255.0\n"+
			"ip dhcp excluded-address 10.15.38.1 10.15.38.19\nip dhcp excluded-address 10.15.38.200 10.15.38.254\nip dhcp pool dhcp60\n"+
			"network 10.15.38.0 255.255.255.0\ndefault-router 10.15.38.4\nlease 7\n"+
			"interface Vlan60\nip address 10.15.38.4 255.255.255.0\n"+
			"end\nexit\n",
		receiveCommand(t, commands),
	)
}

// Stands in for the switch: it accepts connections on the port, and sends what each one wrote on the channel it
// returns once that connection has gone quiet. The socket is open by the time this returns.
func mockTelnet(t *testing.T, port int) chan string {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.Nil(t, err)
	t.Cleanup(func() { ln.Close() })
	commands := make(chan string, 10)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// The test is over and has closed the socket.
				return
			}
			conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			var reader bytes.Buffer
			reader.ReadFrom(conn)
			conn.Close()
			commands <- reader.String()
		}
	}()
	return commands
}

// Returns what the next connection to the mock switch wrote.
func receiveCommand(t *testing.T, commands chan string) string {
	select {
	case command := <-commands:
		return command
	case <-time.After(5 * time.Second):
		assert.Fail(t, "the switch was not sent a command")
		return ""
	}
}

// Asserts that nothing more was sent to the mock switch.
func assertNoCommand(t *testing.T, commands chan string) {
	select {
	case command := <-commands:
		assert.Fail(t, "the switch was sent a command that was not expected", command)
	case <-time.After(50 * time.Millisecond):
	}
}
