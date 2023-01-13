package main

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

var redirMap = make(map[int]string)

func redirector() {
	r := chi.NewRouter()
	r.Get("/{id}.mp4", redirect)
	http.ListenAndServe(":9372", r)
}

func redirect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		return
	}
	value, exists := redirMap[id]
	if !exists {
		return
	}
	http.Redirect(w, r, value, http.StatusFound)
}
