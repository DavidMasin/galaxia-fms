// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Publish-subscribe model for nonblocking notification of server events to websocket clients.

package websocket

import (
	"encoding/json"
	"log"
	"sync"
)

// Allow the listeners to buffer a small number of notifications to streamline delivery.
const notifyBufferSize = 5

type Notifier struct {
	messageType     string
	messageProducer func() any
	listeners       map[chan messageEnvelope]struct{} // The map is essentially a set; the value is ignored.
	mutex           sync.Mutex
	// Guards what the messageProducer reads, if anything does. Whoever calls Notify must already hold it; the
	// notifier takes it itself only to produce the message a newly connected client starts with.
	producerLocker sync.Locker
}

// A message on its way to listeners. The body is already written out as JSON: it is made by whoever notifies, who
// owns the state it describes at that moment, and not later by each listener, who does not.
type messageEnvelope struct {
	messageType string
	messageBody json.RawMessage
}

func NewNotifier(messageType string, messageProducer func() any) *Notifier {
	notifier := &Notifier{messageType: messageType, messageProducer: messageProducer}
	notifier.listeners = make(map[chan messageEnvelope]struct{})
	return notifier
}

// SetProducerLocker names the lock that guards what the messageProducer reads. Notify is then to be called with that
// lock held, and a newly connected client's first message is produced under it.
func (notifier *Notifier) SetProducerLocker(locker sync.Locker) {
	notifier.producerLocker = locker
}

// Calls the messageProducer function and sends a message containing the results to all registered listeners, and cleans
// up any listeners that have closed. The caller must own whatever the messageProducer reads.
func (notifier *Notifier) Notify() {
	notifier.NotifyWithMessage(notifier.getMessageBody())
}

// Sends the given message to all registered listeners, and cleans up any listeners that have closed. If there is a
// messageProducer function defined it is ignored. The message is written out as JSON here and now, so it may refer
// to state that only the caller may read.
func (notifier *Notifier) NotifyWithMessage(messageBody any) {
	encoded, err := json.Marshal(messageBody)
	if err != nil {
		log.Printf("Failed to encode a '%s' notification: %v", notifier.messageType, err)
		return
	}

	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()

	message := messageEnvelope{messageType: notifier.messageType, messageBody: encoded}
	for listener := range notifier.listeners {
		notifier.notifyListener(listener, message)
	}
}

func (notifier *Notifier) notifyListener(listener chan messageEnvelope, message messageEnvelope) {
	defer func() {
		// If channel is closed sending to it will cause a panic; recover and remove it from the list.
		if r := recover(); r != nil {
			delete(notifier.listeners, listener)
		}
	}()

	// Do a non-blocking send. This guarantees that sending notifications won't interrupt the main event loop,
	// at the risk of clients missing some messages if they don't read them all promptly.
	select {
	case listener <- message:
		// The notification was sent and received successfully.
	default:
		log.Printf("Failed to send a '%s' notification due to blocked listener.", notifier.messageType)
	}
}

// Registers and returns a channel that can be read from to receive notification messages. The caller is
// responsible for closing the channel, which will cause it to be reaped from the list of listeners.
func (notifier *Notifier) listen() chan messageEnvelope {
	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()

	listener := make(chan messageEnvelope, notifyBufferSize)
	notifier.listeners[listener] = struct{}{}
	return listener
}

// Removes a listener and closes its channel. Notifying holds the same mutex while it sends, so nothing is sent on
// the channel while or after it is closed.
func (notifier *Notifier) unlisten(listener chan messageEnvelope) {
	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()

	if _, ok := notifier.listeners[listener]; ok {
		delete(notifier.listeners, listener)
		close(listener)
	}
}

// Invokes the message producer to get the message, or returns nil if no producer is defined.
func (notifier *Notifier) getMessageBody() any {
	if notifier.messageProducer == nil {
		return nil
	} else {
		return notifier.messageProducer()
	}
}

// Produces the current message as JSON for a client that has just connected, holding the producer's lock while the
// state is read and written out.
func (notifier *Notifier) snapshot() (json.RawMessage, error) {
	if notifier.producerLocker != nil {
		notifier.producerLocker.Lock()
		defer notifier.producerLocker.Unlock()
	}
	return json.Marshal(notifier.getMessageBody())
}
