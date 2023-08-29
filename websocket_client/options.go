package websocketclient

import "time"

type Option func(*Client)

func WithErrorHandler(handler func(error)) Option {
	return func(c *Client) {
		c.OnError = handler
	}
}

func WithSendQueueSize(size int) Option {
	return func(c *Client) {
		c.sendQueue = make(chan []byte, size)
	}
}

func WithOnConnected(handler func()) Option {
	return func(c *Client) {
		c.OnConnected = handler
	}
}

func WithOnDisconnected(handler func()) Option {
	return func(c *Client) {
		c.OnDisconnected = handler
	}
}

func WithSendBufferSize(size int) Option {
	return func(c *Client) {
		c.sendBufferSize = size
	}
}

func WithReceiveBufferSize(size int) Option {
	return func(c *Client) {
		c.receiveBufferSize = size
	}
}

func WithRetryTimes(retryTimes int) Option {
	return func(c *Client) {
		c.retryTimes = retryTimes
	}
}

func WithHandshakeTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.handshakeTimeout = timeout
	}
}

func WithReconnectInterval(timeout time.Duration) Option {
	return func(c *Client) {
		c.reconnectInterval = timeout
	}
}
