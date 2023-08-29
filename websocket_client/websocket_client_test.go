package websocketclient

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func startTestServer() *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				for {
					messageType, p, err := c.ReadMessage()
					if err != nil {
						return
					}
					if err := c.WriteMessage(messageType, p); err != nil {
						return
					}
				}
			},
		),
	)
}

func TestConnection(t *testing.T) {
	server := startTestServer()
	defer server.Close()

	client := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), zap.NewNop())
	err := client.Connect()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	client.Shutdown()
}

func TestSendReceive(t *testing.T) {
	server := startTestServer()
	defer server.Close()

	client := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), zap.NewNop())
	client.Connect()
	defer client.Shutdown()

	message := []byte("test")
	client.Send(message)

	received := <-client.GetMessages()
	if !bytes.Equal(message, received) {
		t.Fatalf("expected %s, got %s", message, received)
	}
}

func TestShutdown(t *testing.T) {
	server := startTestServer()
	defer server.Close()

	client := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), zap.NewNop())
	client.Connect()

	go func() {
		time.Sleep(time.Millisecond * 50)
		client.Shutdown()
	}()

	select {
	case _, ok := <-client.GetMessages():
		if ok {
			t.Fatalf("expected channel to be closed, but it was open")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("expected channel to be closed within 1 second")
	}
}

func TestReconnection(t *testing.T) {
	connections := 0
	server := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				connections++
				_, _, err = conn.ReadMessage()
				conn.Close() // Close connection to simulate a drop
			},
		),
	)

	defer server.Close()

	client := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), zap.NewNop())
	client.Connect()
	defer client.Shutdown()

	fmt.Println("here")

	client.Send([]byte("test")) // Trigger connection

	time.Sleep(time.Millisecond * 100) // Give some time for reconnection

	if connections < 2 { // At least two connections must be made: initial and after reconnect
		t.Fatalf("expected at least 2 connections, got %d", connections)
	}
}

func TestConnectionError(t *testing.T) {
	errorTriggered := false
	client := NewClient(
		"ws://localhost:40000", zap.NewNop(), WithErrorHandler(
			func(err error) {
				errorTriggered = true
			},
		),
	)
	err := client.Connect()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}

	if !errorTriggered {
		t.Fatalf("expected error handler to be triggered")
	}
}

func TestMessageResendOnReconnect(t *testing.T) {
	var wg sync.WaitGroup

	connections := 0
	server := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				connections++
				if connections == 1 { // Close the connection on the first attempt to simulate a drop
					fmt.Println("closing connection")
					conn.Close()
					wg.Done() // Connection dropped
					return
				}
				fmt.Println("getting message")
				_, data, err := conn.ReadMessage()
				fmt.Println("got message ", string(data))
				if err != nil {
					t.Fatal(err)
				}
				wg.Done() // Message received
			},
		),
	)

	defer server.Close()

	client := NewClient(
		"ws"+strings.TrimPrefix(server.URL, "http"), zap.NewNop(), WithRetryTimes(5),
	)

	wg.Add(2) // Expect two actions: connection drop and message receipt

	err := client.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	message := []byte("test")
	client.Send(message) // Trigger connection, and drop

	wg.Wait() // Wait for both connection drop and message receipt

	if connections != 2 { // Two connections must be made: initial and after reconnect
		t.Fatalf("expected 2 connections, got %d", connections)
	}
}
