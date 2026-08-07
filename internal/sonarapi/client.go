package sonarapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AudioDevice mirrors one entry from GET /audioDevices.
type AudioDevice struct {
	FriendlyName string `json:"friendlyName"`
	ID           string `json:"id"`
	DataFlow     string `json:"dataFlow"`
	Role         string `json:"role"`
	State        string `json:"state"`
}

type audioDevicesResponse struct {
	Value []AudioDevice `json:"value"`
}

// Redirection mirrors one entry from GET /classicRedirections — which
// physical device currently backs a given virtual channel (game, chat,
// media, aux, mic).
type Redirection struct {
	ID        string `json:"id"`
	DeviceID  string `json:"deviceId"`
	IsRunning bool   `json:"isRunning"`
}

type redirectionsResponse struct {
	Value []Redirection `json:"value"`
}

// OutputChannels are the redirection IDs that should point at the
// headset in Game Mode. "mic" is deliberately excluded — in every test
// so far it stayed correctly pinned to the headset even when the other
// four drifted, so there's been nothing to fix there.
var OutputChannels = []string{"game", "chat", "media", "aux"}

// Client talks to Sonar's local REST API on a discovered port.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client for a Sonar instance listening on the given
// port (see DiscoverPort).
func NewClient(port int) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://localhost:%d", port),
		http:    &http.Client{Timeout: 3 * time.Second},
	}
}

// GetAudioDevices returns every audio endpoint Sonar currently knows
// about. The API has been observed returning a bare JSON array here
// rather than the {"value": [...]} wrapper shape — handles both so a
// future API tweak (or an endpoint that does wrap) doesn't break this.
func (c *Client) GetAudioDevices() ([]AudioDevice, error) {
	body, err := c.getRaw("/audioDevices")
	if err != nil {
		return nil, err
	}

	var devices []AudioDevice
	if err := json.Unmarshal(body, &devices); err == nil {
		return devices, nil
	}

	var wrapped audioDevicesResponse
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("unmarshalling /audioDevices (tried both array and wrapped shapes): %w", err)
	}
	return wrapped.Value, nil
}

// GetClassicRedirections returns the current device assignment for
// every virtual channel (game, chat, media, aux, mic). Same
// array-or-wrapped defensive handling as GetAudioDevices.
func (c *Client) GetClassicRedirections() ([]Redirection, error) {
	body, err := c.getRaw("/classicRedirections")
	if err != nil {
		return nil, err
	}

	var redirections []Redirection
	if err := json.Unmarshal(body, &redirections); err == nil {
		return redirections, nil
	}

	var wrapped redirectionsResponse
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("unmarshalling /classicRedirections (tried both array and wrapped shapes): %w", err)
	}
	return wrapped.Value, nil
}

// SetClassicRedirection reassigns a channel (e.g. "game") to the given
// device ID. This is exactly what GG's "Switch it" button does.
func (c *Client) SetClassicRedirection(channel, deviceID string) error {
	url := fmt.Sprintf("%s/classicRedirections/%s/deviceId/%s", c.baseURL, channel, deviceID)
	req, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d setting %s redirection", resp.StatusCode, channel)
	}
	return nil
}

func (c *Client) getRaw(path string) ([]byte, error) {
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
	}
	return io.ReadAll(resp.Body)
}

// classicDeviceState mirrors one "classic" sub-object under
// /volumeSettings/classic's "devices" map — e.g. devices.chatCapture.classic.
type classicDeviceState struct {
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted"`
}

type volumeDeviceEntry struct {
	Classic classicDeviceState `json:"classic"`
}

type volumeSettingsResponse struct {
	Devices map[string]volumeDeviceEntry `json:"devices"`
}

// ChatCaptureChannel is the volumeSettings device name for the
// microphone signal going out to chat/voice apps — confirmed by hand to
// be the channel that actually silences audio (as opposed to
// "classicRedirections"' "mic", which is a different, device-routing
// concept).
const ChatCaptureChannel = "chatCapture"

// IsMicMuted reads the current chatCapture mute state directly from
// Sonar, rather than trusting a locally-tracked boolean that could drift
// out of sync if mute is toggled some other way (GG's own UI, the
// physical headset, etc).
func (c *Client) IsMicMuted() (bool, error) {
	body, err := c.getRaw("/volumeSettings/classic")
	if err != nil {
		return false, err
	}

	var resp volumeSettingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("unmarshalling /volumeSettings/classic: %w", err)
	}

	entry, ok := resp.Devices[ChatCaptureChannel]
	if !ok {
		return false, fmt.Errorf("no %q entry in /volumeSettings/classic response", ChatCaptureChannel)
	}
	return entry.Classic.Muted, nil
}

// SetMicMuted sets the chatCapture mute state.
func (c *Client) SetMicMuted(muted bool) error {
	url := fmt.Sprintf("%s/volumeSettings/classic/%s/Mute/%t", c.baseURL, ChatCaptureChannel, muted)
	req, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d setting mic mute", resp.StatusCode)
	}
	return nil
}

// ToggleMicMuted reads the current mute state and flips it. Returns the
// new state.
func (c *Client) ToggleMicMuted() (bool, error) {
	current, err := c.IsMicMuted()
	if err != nil {
		return false, fmt.Errorf("reading current mute state: %w", err)
	}
	next := !current
	if err := c.SetMicMuted(next); err != nil {
		return false, fmt.Errorf("setting mute state: %w", err)
	}
	return next, nil
}

// FindHeadsetDeviceID finds the physical headset's render (output)
// device among Sonar's known audio devices — friendly name containing
// "Nova Pro" but NOT "Sonar" (that excludes Sonar's own virtual
// devices, which also reference the headset by name in some fields).
func FindHeadsetDeviceID(devices []AudioDevice) (string, error) {
	for _, d := range devices {
		if d.DataFlow == "render" &&
			strings.Contains(d.FriendlyName, "Nova Pro") &&
			!strings.Contains(d.FriendlyName, "Sonar") {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("could not find headset output device in Sonar's audio device list")
}
