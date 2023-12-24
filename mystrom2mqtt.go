package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"golang.org/x/net/trace"
)

var (
	listenAddress = flag.String("listen",
		":8778",
		"listen address for HTTP API (for debug handlers)")

	mqttBroker = flag.String("mqtt_broker",
		"tcp://dr.lan:1883",
		"MQTT broker address for github.com/eclipse/paho.mqtt.golang")

	mqttPrefix = flag.String("mqtt_topic",
		"github.com/stapelberg/mystrom2mqtt/",
		"MQTT topic prefix")
)

func relayCommandHandler(_ mqtt.Client, m mqtt.Message) {
	log.Printf("mqtt: %s: %q", m.Topic(), string(m.Payload()))
	parts := strings.Split(strings.TrimPrefix(m.Topic(), *mqttPrefix+"cmd/relay/"), "/")
	if len(parts) != 2 {
		log.Printf("parts = %q", parts)
		return
	}
	swtch := parts[0]
	command := parts[1]
	if command == "on" {
		command = "1"
	} else if command == "off" {
		command = "0"
	}

	var u string
	switch swtch {
	case "living":
		u = "http://myStrom-Switch-72AB38/relay?state=" + command
	default:
		log.Printf("unknown switch: %q", swtch)
	}
	resp, err := http.Get(u)
	if err != nil {
		log.Print(err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("unexpected HTTP status: %v", resp.Status)
	}
}

func getReportJSON(hostname string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequest("GET", "http://"+hostname+"/report", nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if want := http.StatusOK; resp.StatusCode != want {
		return nil, fmt.Errorf("unexpected HTTP status: got %v, want %v", resp.Status, want)
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func subscribe(mqttClient mqtt.Client, topic string, hdl mqtt.MessageHandler) error {
	const qosAtMostOnce = 0
	log.Printf("Subscribing to %s", topic)
	token := mqttClient.Subscribe(topic, qosAtMostOnce, hdl)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscription failed: %v", err)
	}
	return nil
}

func mystrom2mqtt() error {
	opts := mqtt.NewClientOptions().AddBroker(*mqttBroker)
	clientID := "https://github.com/stapelberg/mystrom2mqtt"
	if hostname, err := os.Hostname(); err == nil {
		clientID += "@" + hostname
	}
	opts.SetClientID(clientID)
	opts.SetConnectRetry(true)
	opts.OnConnect = func(c mqtt.Client) {
		if err := subscribe(c, *mqttPrefix+"cmd/relay/#", relayCommandHandler); err != nil {
			log.Print(err)
		}
	}
	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("MQTT connection failed: %v", token.Error())
	}

	for name, hostname := range map[string]string{
		"living":        "myStrom-Switch-72AB38",
		"automation":    "myStrom-Switch-A48E4C",
		"lea":           "myStrom-Switch-72B600",
		"monitor":       "myStrom-Switch-A46FD0",
		"midna":         "myStrom-Switch-E33414",
		"pacna":         "myStrom-Switch-A4849C",
		"portabel":      "myStrom-Switch-7D6FDC",
		"solar":         "myStrom-Switch-943DAC",
		"basislagercam": "10.11.0.109",
	} {
		name, hostname := name, hostname // copy
		go func() {
			for range time.Tick(30 * time.Second) {
				report, err := getReportJSON(hostname)
				if err != nil {
					log.Print(err)
					continue
				}
				mqttClient.Publish(
					*mqttPrefix+"report/"+name,
					0,     /* qos */
					false, /* retained */
					string(report))
				log.Printf("published to MQTT")
			}
		}()
	}

	trace.AuthRequest = func(req *http.Request) (any, sensitive bool) { return true, true }

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/requests/", trace.Traces)
	log.Printf("http.ListenAndServe(%q)", *listenAddress)
	if err := http.ListenAndServe(*listenAddress, mux); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()
	if err := mystrom2mqtt(); err != nil {
		log.Fatal(err)
	}
}
