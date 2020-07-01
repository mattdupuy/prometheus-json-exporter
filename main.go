package main

import (
	//"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	//"io/ioutil"
	"log"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yalp/jsonpath"
	"github.com/ovh/go-ovh/ovh"
)

type ReceiverFunc func(key string, value float64)

func (receiver ReceiverFunc) Receive(key string, value float64) {
	receiver(key, value)
}

type Receiver interface {
	Receive(key string, value float64)
}

func WalkJSON(path string, jsonData interface{}, receiver Receiver) {
	switch v := jsonData.(type) {
	case int:
		receiver.Receive(path, float64(v))
	case float64:
		receiver.Receive(path, v)
	case bool:
		n := 0.0
		if v {
			n = 1.0
		}
		receiver.Receive(path, n)
	case string:
		// ignore
	case nil:
		// ignore
	case []interface{}:
		prefix := path + "__"
		for i, x := range v {
			WalkJSON(fmt.Sprintf("%s%d", prefix, i), x, receiver)
		}
	case map[string]interface{}:
		prefix := ""
		if path != "" {
			prefix = path + "_"
		}
		for k, x := range v {
			WalkJSON(fmt.Sprintf("%s%s", prefix, k), x, receiver)
		}
	default:
		log.Printf("unkown type: %#v", v)
	}
}

func doOvhProbe(target string) (interface{}, error){
	ovhClient, err :=ovh.NewEndpointClient("ovh-eu")
	
	if err != nil {
		fmt.Printf("Error: %q\n", err)
	}
	
	// call get function
	bytes := []byte{}
	err = ovhClient.Get(target, &bytes)
	if err != nil {
		fmt.Printf("Error: %q\n", err)
		return nil, err
	}
	var jsonData interface{}
	err = json.Unmarshal([]byte(bytes), &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func ovhProbeHandler(w http.ResponseWriter, r *http.Request) {
	registry := prometheus.NewRegistry()

	params := r.URL.Query()

	prefix := params.Get("prefix")

	target := params.Get("ovhTarget")
	if target == "" {
		http.Error(w, "Target parameter is missing", http.StatusBadRequest)
		return
	}

	jsonData, err := doOvhProbe(target)
	if err != nil {
		log.Print(err)
		// http.Error(w, err.Error(), http.StatusInternalServerError)
		promGaugeGenerate(registry, prefix, "up", "Json API Up status", 0)
	} else {
		lookuppath := params.Get("jsonpath")
		if lookuppath != "" {
			jsonPath, err := jsonpath.Read(jsonData, lookuppath)
			if err != nil {
				http.Error(w, "Jsonpath not found", http.StatusNotFound)
				return
			}
			log.Printf("Found value %v", jsonPath)
			jsonData = jsonPath
		}

		WalkJSON("", jsonData, ReceiverFunc(func(key string, value float64) {
			promGaugeGenerate(registry, prefix, sanitizeKey(key), "Retrieved value", value)
		}))

		promGaugeGenerate(registry, prefix, "up", "Json API Up status", 1)
	}

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func sanitizeKey(key string) string {
	r := strings.NewReplacer(
		" ", "_",
		"/", "_",
		":", "_")
	return r.Replace(key)
}

func promGaugeGenerate(registry *prometheus.Registry, prefix, key, help string, value float64) {
	g := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: prefix + key,
			Help: help,
		},
	)
	registry.MustRegister(g)
	g.Set(value)
}

var indexHTML = []byte(`<html>
<head><title>Json Exporter</title></head>
<body>
<h1>Json Exporter</h1>
<p><a href="/ovhprobe">Run OVH probe</a></p>
<p><a href="/metrics">Metrics</a></p>
</body>
</html>`)

func main() {
	addr := flag.String("listen-address", ":9116", "The address to listen on for HTTP requests.")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(indexHTML)
	})
	http.HandleFunc("/ovhprobe", ovhProbeHandler)
	http.Handle("/metrics", promhttp.Handler())

	log.Printf("listenning on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
