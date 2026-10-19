package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi"
	kitlog "github.com/go-kit/kit/log"

	shipping "github.com/marcusolsson/goddd"
	"github.com/marcusolsson/goddd/booking"
)

// Server 保存 booking HTTP 服务的依赖。
type Server struct {
	Booking booking.Service

	Logger kitlog.Logger

	router chi.Router
}

// New 创建仅包含 booking 路由的 HTTP 服务。
func New(bs booking.Service, logger kitlog.Logger) *Server {
	s := &Server{
		Booking: bs,
		Logger:  logger,
	}

	r := chi.NewRouter()

	r.Use(accessControl)

	r.Route("/booking", func(r chi.Router) {
		h := bookingHandler{s.Booking, s.Logger}
		r.Mount("/v1", h.router())
	})
	s.router = r

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func accessControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type")

		if r.Method == "OPTIONS" {
			return
		}

		h.ServeHTTP(w, r)
	})
}

func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch err {
	case shipping.ErrUnknownCargo:
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": err.Error(),
	})
}
