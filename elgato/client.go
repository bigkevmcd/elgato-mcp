// Package elgato is a client for the local HTTP API exposed by Elgato Key
// Light and Key Light Air devices.
package elgato

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"time"
)

// DefaultPort is the port the Elgato lights serve their API on.
const DefaultPort = "9123"

// Limits accepted by the light hardware.
const (
	MinBrightness = 0
	MaxBrightness = 100
	MinKelvin     = 2900
	MaxKelvin     = 7000
)

// State is the state of a single light as reported by the device.
//
// Temperature is in the device's native units (143-344), use Kelvin and
// KelvinToTemperature to convert.
type State struct {
	On          int `json:"on"`
	Brightness  int `json:"brightness"`
	Temperature int `json:"temperature"`
}

// Kelvin returns the colour temperature of the light in Kelvin.
func (s State) Kelvin() int {
	if s.Temperature == 0 {
		return 0
	}

	return int(math.Round(1_000_000 / float64(s.Temperature)))
}

// KelvinToTemperature converts a Kelvin value to the device's native units.
func KelvinToTemperature(k int) int {
	return int(math.Round(1_000_000 / float64(k)))
}

// AccessoryInfo describes the device.
type AccessoryInfo struct {
	ProductName         string `json:"productName"`
	SerialNumber        string `json:"serialNumber"`
	FirmwareVersion     string `json:"firmwareVersion"`
	DisplayName         string `json:"displayName"`
	HardwareBoardType   int    `json:"hardwareBoardType"`
	FirmwareBuildNumber int    `json:"firmwareBuildNumber"`
}

type lightsDoc struct {
	NumberOfLights int     `json:"numberOfLights"`
	Lights         []State `json:"lights"`
}

// Client talks to a single Elgato light.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a client for the light at addr, which is a host with an
// optional port; DefaultPort is used if no port is provided.
func NewClient(addr string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	return &Client{baseURL: "http://" + NormalizeAddr(addr), http: httpClient}
}

// NormalizeAddr adds DefaultPort to addr if it doesn't include a port.
func NormalizeAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, DefaultPort)
	}

	return addr
}

// AccessoryInfo fetches the device description.
func (c *Client) AccessoryInfo(ctx context.Context) (*AccessoryInfo, error) {
	var info AccessoryInfo
	if err := c.do(ctx, http.MethodGet, "/elgato/accessory-info", nil, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// State fetches the current state of the light.
func (c *Client) State(ctx context.Context) (State, error) {
	var doc lightsDoc
	if err := c.do(ctx, http.MethodGet, "/elgato/lights", nil, &doc); err != nil {
		return State{}, err
	}

	if len(doc.Lights) == 0 {
		return State{}, fmt.Errorf("%s reported no lights", c.baseURL)
	}

	return doc.Lights[0], nil
}

// SetState updates the light and returns the state reported by the device.
func (c *Client) SetState(ctx context.Context, s State) (State, error) {
	var doc lightsDoc

	body := lightsDoc{NumberOfLights: 1, Lights: []State{s}}
	if err := c.do(ctx, http.MethodPut, "/elgato/lights", body, &doc); err != nil {
		return State{}, err
	}

	if len(doc.Lights) == 0 {
		return s, nil
	}

	return doc.Lights[0], nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body *bytes.Reader

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}

		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s%s: unexpected status %s", method, c.baseURL, path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response from %s%s: %w", c.baseURL, path, err)
	}

	return nil
}
