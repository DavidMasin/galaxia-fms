package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// verifySignature checks a delivery the way the documentation tells a receiver to: the HMAC-SHA256 of the
// timestamp, a full stop and the raw body, keyed with the secret, compared in constant time, and a timestamp no more
// than five minutes from the receiver's clock. It uses nothing from the package under test.
func verifySignature(secret string, header http.Header, body []byte, now time.Time) error {
	timestamp := header.Get("X-Galaxia-Timestamp")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("the timestamp %q is not a number", timestamp)
	}
	if age := now.Sub(time.Unix(seconds, 0)); age > 5*time.Minute || age < -5*time.Minute {
		return fmt.Errorf("the timestamp is %v away from now", age)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(header.Get("X-Galaxia-Signature"))) {
		return fmt.Errorf("the signature does not match")
	}
	return nil
}

// received is one request as the test receiver saw it.
type received struct {
	At        time.Time // Real time of arrival.
	Event     string
	Delivery  string
	Attempt   string
	Timestamp string
	Body      []byte
	Problem   string // Why the signature was refused; empty if it was good.
}

// answer is what the receiver does with its n-th request (counting from 1): wait, then answer with a status.
type answer func(n int) (status int, wait time.Duration)

// testReceiver is a webhook receiver on a local HTTP server.
type testReceiver struct {
	URL      string
	secret   func() string
	now      func() time.Time
	mu       sync.Mutex
	requests []received
	answer   answer
}

func newTestReceiver(t *testing.T, secret func() string, now func() time.Time, answer answer) *testReceiver {
	receiver := &testReceiver{secret: secret, now: now, answer: answer}
	server := httptest.NewServer(http.HandlerFunc(receiver.serve))
	t.Cleanup(server.Close)
	receiver.URL = server.URL + "/hook"
	return receiver
}

func (receiver *testReceiver) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	request := received{
		At: time.Now(), Event: r.Header.Get("X-Galaxia-Event"), Delivery: r.Header.Get("X-Galaxia-Delivery"),
		Attempt: r.Header.Get("X-Galaxia-Attempt"), Timestamp: r.Header.Get("X-Galaxia-Timestamp"), Body: body,
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		request.Problem = "not a JSON POST"
	} else if err := verifySignature(receiver.secret(), r.Header, body, receiver.now()); err != nil {
		request.Problem = err.Error()
	}
	receiver.mu.Lock()
	receiver.requests = append(receiver.requests, request)
	n := len(receiver.requests)
	answer := receiver.answer
	receiver.mu.Unlock()

	if request.Problem != "" {
		http.Error(w, request.Problem, http.StatusUnauthorized)
		return
	}
	status, wait := http.StatusOK, time.Duration(0)
	if answer != nil {
		status, wait = answer(n)
	}
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, "answer %d", n)
}

func (receiver *testReceiver) received() []received {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return append([]received(nil), receiver.requests...)
}

func (receiver *testReceiver) setAnswer(answer answer) {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	receiver.answer = answer
}

// always answers every request the same way.
func always(status int) answer {
	return func(int) (int, time.Duration) { return status, 0 }
}
