// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package websocket

import (
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"log"
	"testing"
)

func TestNotifier(t *testing.T) {
	notifier := NewNotifier("testMessageType", generateTestMessage)

	// Should do nothing when there are no listeners.
	notifier.Notify()
	notifier.NotifyWithMessage(12345)
	notifier.NotifyWithMessage(struct{}{})

	listener := notifier.listen()
	notifier.Notify()
	message := <-listener
	assert.Equal(t, "testMessageType", message.messageType)
	assert.Equal(t, `"test message"`, string(message.messageBody))
	notifier.NotifyWithMessage(12345)
	assert.Equal(t, "12345", string((<-listener).messageBody))

	// Should allow multiple messages without blocking.
	notifier.NotifyWithMessage("message1")
	notifier.NotifyWithMessage("message2")
	notifier.Notify()
	assert.Equal(t, `"message1"`, string((<-listener).messageBody))
	assert.Equal(t, `"message2"`, string((<-listener).messageBody))
	assert.Equal(t, `"test message"`, string((<-listener).messageBody))

	// Should stop sending messages and not block once the buffer is full.
	log.SetOutput(ioutil.Discard) // Silence noisy log output.
	for i := 0; i < 20; i++ {
		notifier.NotifyWithMessage(i)
	}
	var value messageEnvelope
	var lastValue string
	for lastValue == "" {
		select {
		case value = <-listener:
		default:
			lastValue = string(value.messageBody)
		}
	}
	// The buffer holds five messages, so the last one that got through is the fifth: number 4.
	assert.Equal(t, "4", lastValue)
	notifier.NotifyWithMessage("next message")
	assert.Equal(t, `"next message"`, string((<-listener).messageBody))
}

func TestNotifyMultipleListeners(t *testing.T) {
	notifier := NewNotifier("testMessageType2", nil)
	listeners := [50]chan messageEnvelope{}
	for i := 0; i < len(listeners); i++ {
		listeners[i] = notifier.listen()
	}

	notifier.Notify()
	notifier.NotifyWithMessage(12345)
	for listener := range notifier.listeners {
		assert.Equal(t, "null", string((<-listener).messageBody))
		assert.Equal(t, "12345", string((<-listener).messageBody))
	}

	// Should reap closed channels automatically.
	close(listeners[4])
	notifier.NotifyWithMessage("message1")
	assert.Equal(t, 49, len(notifier.listeners))
	for listener := range notifier.listeners {
		assert.Equal(t, `"message1"`, string((<-listener).messageBody))
	}
	close(listeners[16])
	close(listeners[21])
	close(listeners[49])
	notifier.NotifyWithMessage("message2")
	assert.Equal(t, 46, len(notifier.listeners))
	for listener := range notifier.listeners {
		assert.Equal(t, `"message2"`, string((<-listener).messageBody))
	}

	// A listener that leaves is removed and its channel closed, and leaving twice is harmless.
	notifier.unlisten(listeners[0])
	notifier.unlisten(listeners[0])
	assert.Equal(t, 45, len(notifier.listeners))
	_, open := <-listeners[0]
	assert.False(t, open)
	notifier.NotifyWithMessage("message3")
	assert.Equal(t, 45, len(notifier.listeners))
}

// A message that cannot be written as JSON is dropped rather than sent broken.
func TestNotifyUnencodable(t *testing.T) {
	notifier := NewNotifier("testMessageType3", nil)
	listener := notifier.listen()
	log.SetOutput(ioutil.Discard)
	notifier.NotifyWithMessage(make(chan int))
	notifier.NotifyWithMessage("after")
	assert.Equal(t, `"after"`, string((<-listener).messageBody))
}

// The producer's lock is taken to make a new client's first message, and never by Notify, whose caller holds it.
func TestProducerLocker(t *testing.T) {
	var locker countingLocker
	notifier := NewNotifier("testMessageType4", generateTestMessage)
	notifier.SetProducerLocker(&locker)

	listener := notifier.listen()
	notifier.Notify()
	assert.Equal(t, `"test message"`, string((<-listener).messageBody))
	assert.Equal(t, 0, locker.locks)

	body, err := notifier.snapshot()
	assert.Nil(t, err)
	assert.Equal(t, `"test message"`, string(body))
	assert.Equal(t, 1, locker.locks)
	assert.Equal(t, 1, locker.unlocks)
}

type countingLocker struct {
	locks, unlocks int
}

func (locker *countingLocker) Lock() {
	locker.locks++
}

func (locker *countingLocker) Unlock() {
	locker.unlocks++
}

func generateTestMessage() any {
	return "test message"
}
