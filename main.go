package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
	"unicode/utf16"

	"github.com/google/gousb"
)

var colorData2 = []byte{
	0x56, 0x83, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0xc1, 0x00, 0x00, 0x00, 0x00,
	0xaa, 0xff, 0xff, // Color
	0x33, // Brightness
}

var colorData1 = []byte{
	0x56, 0x81, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xaa, 0xaa, 0xaa, 0xaa,
}

var colorData3 = []byte{
	0x51, 0x28, 0x00, 0x00,
	0xff, // Pattern
}

var changeSettingMaybe = []byte{
	0x41, 0x01,
}

type DuckyKB struct {
	OutEP *gousb.OutEndpoint
	InEP  *gousb.InEndpoint
}

type alertManagerAlertJSON struct {
	Annotations struct {
		Description string `json:"description"`
		Summary     string `json:"summary"`
	} `json:"annotations"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Labels       map[string]string `json:"labels"`
	StartsAt     string            `json:"startsAt"`
	Status       string            `json:"status"`
}

type alertManagerJSON struct {
	Alerts            []alertManagerAlertJSON `json:"alerts"`
	CommonAnnotations struct {
		Summary string `json:"summary"`
	} `json:"commonAnnotations"`
	CommonLabels struct {
		Alertname string `json:"alertname"`
	} `json:"commonLabels"`
	ExternalURL string `json:"externalURL"`
	GroupKey    string `json:"groupKey"`
	GroupLabels struct {
		Alertname string `json:"alertname"`
	} `json:"groupLabels"`
	Receiver string `json:"receiver"`
	Status   string `json:"status"`
	Version  string `json:"version"`
}

var currentAlerts int

func main() {
	listenAddr := flag.String("listen", ":9095", "the listen address for http")
	flag.Parse()

	ctx := gousb.NewContext()
	defer ctx.Close()

	dev, err := ctx.OpenDeviceWithVIDPID(0x04d9, 0x0348)
	if err != nil || dev == nil {
		log.Fatalf("failed to open usb device: %v", err)
	}

	err = dev.SetAutoDetach(true)
	if err != nil {
		log.Fatal(err)
	}
	defer dev.SetAutoDetach(false)

	cfg, err := dev.Config(1)
	if err != nil {
		log.Fatal(err)
	}
	defer cfg.Close()

	intf, err := cfg.Interface(1, 0)
	if err != nil {
		log.Fatal("Interface(): ", err)
	}
	defer intf.Close()

	ep, err := intf.OutEndpoint(4)
	if err != nil {
		log.Fatal("OutEndpoint(): ", err)
	}

	inEp, err := intf.InEndpoint(3)
	if err != nil {
		log.Fatal("InEndpoint(): ", err)
	}

	kb := &DuckyKB{
		InEP:  inEp,
		OutEP: ep,
	}

	fmt.Printf("Firmware Version: %s\n", kb.GetFirmwareVersion())

	currentAlerts = 0

	go func() {
		lastAlerts := -1
		for {
			if currentAlerts > 0 {
				lastAlerts = currentAlerts
				kb.SetBacklight(0x00, 0x00, 0x00, 0x00, DuckyKBPatternStatic)
				time.Sleep(time.Millisecond * 500)
				kb.SetBacklight(0xff, 0x00, 0x00, 0xff, DuckyKBPatternStatic)
				time.Sleep(time.Millisecond * 500)
			} else {
				if lastAlerts != 0 {
					kb.SetBacklight(0xff, 0xff, 0xff, 0xff, DuckyKBPatternRainbow)
				}
				lastAlerts = currentAlerts
				time.Sleep(time.Millisecond * 500)
			}
		}
	}()

	http.HandleFunc("/webhook/am", func(w http.ResponseWriter, r *http.Request) {
		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
		}

		var am alertManagerJSON
		err = json.Unmarshal(b, &am)
		if err != nil {
			log.Println(err)
		}

		for _, alert := range am.Alerts {
			log.Printf("%s: %s", alert.Status, alert.Annotations.Summary)
			if alert.Status == "resolved" {
				currentAlerts--
				if currentAlerts < 0 {
					currentAlerts = 0
				}
			} else if alert.Status == "firing" {
				currentAlerts++
			}
		}
	})

	http.ListenAndServe(*listenAddr, nil)
}

func (kb *DuckyKB) WriteToDev(data []byte) (int, error) {
	dataLen := len(data)

	for i := 0; i < 64-dataLen; i++ {
		data = append(data, 0x00)
	}

	n, err := kb.OutEP.Write(data)
	return n, err
}

func (kb *DuckyKB) ReadFromDev() ([]byte, error) {
	buf := make([]byte, 64)
	n, err := kb.InEP.Read(buf)
	if err != nil {
		return nil, nil
	}
	//log.Printf("read: %x\n", buf[:n])
	return buf[:n], nil
}

func (kb *DuckyKB) GetFirmwareVersion() string {
	_, err := kb.WriteToDev(usbPacketGetVersion)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := kb.ReadFromDev()
	if err != nil {
		log.Fatal(err)
	}

	// The version string is UTF-16LE and starts at pos 9
	var tmp []uint16
	d := resp[8:]
	for i := 0; i < len(d)/2; i++ {
		j := binary.LittleEndian.Uint16([]byte{d[i*2], d[i*2+1]})
		if j == 0 {
			break
		}
		tmp = append(tmp, j)
	}
	runes := utf16.Decode(tmp)

	return string(runes)
}

func (kb *DuckyKB) SetBacklight(r int, g int, b int, brightness int, pattern int) {
	kb.WriteToDev(changeSettingMaybe)
	d, _ := kb.ReadFromDev()
	log.Print(d)

	kb.WriteToDev(colorData1)
	d, _ = kb.ReadFromDev()
	log.Print(d)

	kb.WriteToDev(append(usbPacketSetColorPrefix, byte(r), byte(g), byte(b), byte(brightness)))
	d, _ = kb.ReadFromDev()
	log.Print(d)

	kb.WriteToDev(append(usbPacketSetPatternPrefix, byte(pattern)))
	d, _ = kb.ReadFromDev()
	log.Print(d)
}
