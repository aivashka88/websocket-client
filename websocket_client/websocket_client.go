package websocketclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type ConnectionState int

const (
	Disconnected ConnectionState = iota
	Connecting
	Connected
)

type Client struct {
	addr     string
	logger   *zap.Logger
	messages chan []byte

	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	connState int32
	errCh     chan error
	wg        sync.WaitGroup
	mu        sync.Mutex

	// Options
	OnError           func(error)
	OnConnected       func()
	OnDisconnected    func()
	sendQueue         chan []byte
	sendBufferSize    int
	receiveBufferSize int
	retryTimes        int
	handshakeTimeout  time.Duration
	reconnectInterval time.Duration
}

func NewClient(addr string, logger *zap.Logger, options ...Option) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		addr:              addr,
		logger:            logger,
		messages:          make(chan []byte),
		ctx:               ctx,
		cancel:            cancel,
		errCh:             make(chan error, 1),
		sendBufferSize:    1024,                 // default
		receiveBufferSize: 1024,                 // default
		sendQueue:         make(chan []byte, 5), // default
		retryTimes:        2,                    // default
		handshakeTimeout:  5 * time.Second,      // default
		reconnectInterval: 2 * time.Second,      // default
		OnError:           nil,
		OnConnected:       nil,
		OnDisconnected:    nil,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

func (c *Client) Send(message []byte) error {
	if c.getConnectionState() != Connected {
		c.logger.Warn("Attempted to send a message without being connected.")
		return fmt.Errorf("client is not connected")
	}
	c.sendQueue <- message
	return nil
}

func (c *Client) GetMessages() <-chan []byte {
	return c.messages
}

func (c *Client) Connect() error {
	defer func() {
		go c.manageConnection()
		go c.readMessages()
		go c.sendMessages()
	}()
	return c.connect()
}

func (c *Client) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel()
	c.wg.Wait() // Wait for all goroutines to finish
	if c.conn != nil {
		_ = c.conn.Close()
	}
	close(c.sendQueue)
	close(c.errCh)
	close(c.messages)
}

func (c *Client) setConnectionState(state ConnectionState) {
	atomic.StoreInt32(&c.connState, int32(state))
}

func (c *Client) getConnectionState() ConnectionState {
	return ConnectionState(atomic.LoadInt32(&c.connState))
}

func (c *Client) connect() error {
	c.setConnectionState(Connecting)
	dialer := websocket.Dialer{
		HandshakeTimeout: c.handshakeTimeout,
		ReadBufferSize:   c.receiveBufferSize,
		WriteBufferSize:  c.sendBufferSize,
	}
	now := time.Now()
	c.logger.Info("Sending connect")
	conn, _, err := dialer.Dial(c.addr, nil)
	if err != nil {
		duration := time.Since(now)
		c.logger.Error(
			"Connection failed ", zap.Error(err), zap.String("address", c.addr),
			zap.Duration("connectionDuration", duration),
		)
		c.setConnectionState(Disconnected)
		if c.OnError != nil {
			c.OnError(err)
		}
		return err
	}
	duration := time.Since(now)
	c.logger.Info(
		"Successfully connected ", zap.String("address", c.addr),
		zap.Duration("connectionDuration", duration),
	)
	c.conn = conn
	c.setConnectionState(Connected)
	if c.OnConnected != nil {
		c.OnConnected()
	}
	return nil
}

func (c *Client) readMessages() {
	c.wg.Add(1)
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			if c.getConnectionState() != Connected {
				c.logger.Debug("connection is not ready, waiting connection state before reading")
				time.Sleep(time.Second * 1) // Wait if the connection is not ready.
				continue
			}
			deadline := time.Now().Add(5 * time.Second)
			_ = c.conn.SetReadDeadline(deadline) // Setting deadline, so we can reconnect if the connection hangs.
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				if c.OnError != nil {
					c.OnError(err)
				}
				c.logger.Error("failed to read message", zap.Error(err))
				// Send error to connection manager.
				// The manageConnection method will handle reconnection.
				c.errCh <- err
				continue
			}
			c.messages <- data
		}
	}
}

func (c *Client) sendMessages() {
	c.wg.Add(1)
	defer c.wg.Done()
	retryMap := make(map[string]int)
	for {
		select {
		case <-c.ctx.Done():
			return
		case message := <-c.sendQueue:
			c.logger.Debug(
				"getting ready to send message, ", zap.String("message", string(message)),
			)
			if c.getConnectionState() != Connected {
				c.logger.Debug("connection is not ready, sending message back to the queue")
				c.sendQueue <- message
				time.Sleep(time.Second * 1) // Wait if the connection is not ready.
				continue
			}
			messageID := string(message)
			if retryMap[messageID] >= c.retryTimes {
				c.logger.Error(
					"failed to send message", zap.String("message", messageID),
					zap.Int("retryTimes", c.retryTimes),
				)
				delete(retryMap, messageID)
				continue
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.logger.Error(
					"failed to send message, sending message back to the queue", zap.Error(err),
				)
				c.sendQueue <- message
				retryMap[messageID]++
				if c.OnError != nil {
					c.OnError(err)
				}
				// Send error to connection manager.
				// The manageConnection method will handle reconnection.
				c.errCh <- err
				continue
			}
			c.logger.Debug("message sent successfully", zap.String("message", messageID))
			delete(retryMap, messageID)
		}
	}
}

func (c *Client) manageConnection() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case _ = <-c.errCh:
			c.setConnectionState(Disconnected)
		default:
			switch c.getConnectionState() {
			case Disconnected:
				c.logger.Warn("Got disconnected")
				err := c.connect()
				if err != nil {
					c.logger.Error(
						"failed to connect with error, ", zap.Error(err),
						zap.String("reconnectInterval", c.reconnectInterval.String()),
					)
					time.Sleep(c.reconnectInterval) // Wait before retrying
					continue
				}
			case Connected, Connecting:
				// OK
			}
			time.Sleep(500 * time.Millisecond) // Interval for checking the connection state
		}
	}
}

func isReconnectionRequired(err error) bool {
	if websocket.IsCloseError(
		err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	) {
		return true
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	if err == io.EOF {
		return true
	}
	return false
}

//if isReconnectionRequired(err) {
//c.logger.Error("Critical error occurred, attempting reconnection", zap.Error(err))
//c.errCh <- err
//} else {
//c.logger.Warn("Non-critical error occurred", zap.Error(err))
//// Handle the error without triggering reconnection
//if c.OnError != nil {
//c.OnError(err)
//}
//}
