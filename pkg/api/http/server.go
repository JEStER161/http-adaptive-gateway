package http

import (
	"gateway/configs"
	stdhttp "net/http"
)

func NewServer(handler stdhttp.Handler, config configs.HTTP) *stdhttp.Server {
	return &stdhttp.Server{
		Addr: config.Address(),
		Handler: handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout.Duration,
		ReadTimeout: config.ReadTimeout.Duration,
		WriteTimeout: config.WriteTimeout.Duration,
		IdleTimeout: config.IdleTimeout.Duration,
	}
}
