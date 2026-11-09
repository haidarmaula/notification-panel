//go:build ignore

// The fixture validates the routed Kafka incident and logs only its fingerprint.
package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	log.SetFlags(0)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notifications", receive)
	srv := &http.Server{
		Addr: ":9080", Handler: mux, ReadTimeout: 3 * time.Second,
		WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192,
	}
	log.Fatal(srv.ListenAndServe())
}

func receive(w http.ResponseWriter, r *http.Request) {
	var message struct {
		Receiver        string
		Status          string
		Version         string
		TruncatedAlerts int
		Alerts          []struct {
			Status      string
			Labels      map[string]string
			Annotations map[string]string
			Fingerprint string
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	if decoder.Decode(&message) != nil || decoder.Decode(new(any)) != io.EOF ||
		message.Receiver != "fixture-webhook" || message.Version != "4" ||
		message.TruncatedAlerts != 0 || len(message.Alerts) != 1 ||
		(message.Status != "firing" && message.Status != "resolved") {
		http.Error(w, "unexpected fixture notification", http.StatusBadRequest)
		return
	}
	alert := message.Alerts[0]
	fingerprint, err := hex.DecodeString(alert.Fingerprint)
	if err != nil || len(fingerprint) != 8 || alert.Status != message.Status ||
		alert.Labels["alertname"] != "HelloKafkaOfflinePartitions" ||
		alert.Labels["environment"] != "fixture" || alert.Labels["cluster"] != "fixture" ||
		alert.Labels["severity"] != "critical" ||
		alert.Annotations["runbook"] != "monitoring.md#kafka-replication" {
		http.Error(w, "unexpected fixture alert", http.StatusBadRequest)
		return
	}
	log.Printf("fixture webhook accepted %s %s", alert.Status, alert.Fingerprint)
	w.WriteHeader(http.StatusNoContent)
}
