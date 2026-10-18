package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"example.com/demo-orders/internal/orders"
)

type Handler struct{ service *orders.Service }

func (h *Handler) Place(w http.ResponseWriter, r *http.Request) {
	var order orders.Order
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.service.Place(r.Context(), order.ID, order.SKU, order.Quantity); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Cancel(r.URL.Query().Get("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, orders.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, orders.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, orders.ErrConflict):
		status = http.StatusConflict
	}
	http.Error(w, err.Error(), status)
}
