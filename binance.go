package main

import (
	"go.uber.org/zap"
	"os"
	"os/signal"
	"syscall"
	"test/websocket_client"
)

func main() {

	url := "wss://stream.binance.com:9443/ws/btcusdt@depth"
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to set up logger")
	}
	client := websocketclient.NewClient(
		url,
		logger,
		websocketclient.WithSendQueueSize(2),
		websocketclient.WithOnConnected(func() {
			logger.Info("connected to websocket")
		}),
		websocketclient.WithErrorHandler(func(err error) {
			logger.Error("error", zap.Error(err))
		}),
	)
	err = client.Connect()
	if err != nil {
		logger.Error("error ", zap.Error(err))
		return
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for message := range client.GetMessages() {
			logger.Info("Received message, len in bytes ", zap.Int("Bytes len", len(message)))
		}
	}()
	<-shutdown
	client.Shutdown()
	logger.Info("Shutting down gracefully...")
}
