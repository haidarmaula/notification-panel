package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// The control socket lives in the owner-only checkout state directory. The LAN
// listener never registers the operation that generates pairing codes.
func (s *issuerServer) startDashboardControl(path, origin, publicOrigin string) (func(), error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Public bool `json:"public"`
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 64))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &request); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		linkOrigin := origin
		if request.Public {
			if publicOrigin == "" {
				writeJSON(w, http.StatusBadRequest, apiEnvelope{Success: false, Message: "public pairing origin is not configured"})
				return
			}
			linkOrigin = publicOrigin
		}
		code := make([]byte, 32)
		if _, err := rand.Read(code); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(code)
		link := pairingLink(linkOrigin, linkOrigin, s.cfg.AppID, encoded)
		svg, err := pairingSVG(link)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		now := time.Now()
		for key, session := range s.sessions {
			if !now.Before(session.expires) {
				delete(s.sessions, key)
			}
		}
		if len(s.sessions) >= 256 {
			s.mu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		expires := now.Add(pairingLifetime)
		s.pairingCode, s.pairingExpires = encoded, expires
		s.pairingUsed, s.failedAttempts = false, 0
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, apiEnvelope{Success: true, Data: map[string]any{
			"qr_svg": svg, "expires_at": expires.UTC(), "origin": linkOrigin, "link": link,
		}})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return func() { _ = server.Close() }, nil
}

func (s *issuerServer) handleDashboardOrigin(w http.ResponseWriter, app, bearer string) {
	key := sha256.Sum256([]byte(bearer))
	session, ok := s.sessions[key]
	now := time.Now()
	if app != s.cfg.AppID || !ok || !now.Before(session.expires) || session.origins >= maxOriginAssertions {
		writeJSON(w, http.StatusUnauthorized, apiEnvelope{Success: false, Message: "invalid mobile credential"})
		return
	}
	origin, err := s.originAssertion(bearer, now.UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiEnvelope{Success: false, Message: "issuer unavailable"})
		return
	}
	session.origins++
	s.sessions[key] = session
	writeJSON(w, http.StatusOK, apiEnvelope{Success: true, Data: originResponse{Origin: origin}})
}

func pairingSVG(link string) (string, error) {
	qr, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return "", err
	}
	modules := qr.Bitmap()
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><path fill="white" d="M0 0h%dv%dH0z"/><path d="`, len(modules), len(modules), len(modules), len(modules))
	for y, line := range modules {
		for x, dark := range line {
			if dark {
				fmt.Fprintf(&svg, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	svg.WriteString(`"/></svg>`)
	return svg.String(), nil
}
