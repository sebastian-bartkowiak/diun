package homeassistant

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/crazy-max/diun/v4/internal/model"
	"github.com/crazy-max/diun/v4/internal/notif/notifier"
	"github.com/crazy-max/diun/v4/pkg/utl"
	MQTT "github.com/eclipse/paho.mqtt.golang"
)

// Client represents an active mqtt notification object
type Client struct {
	*notifier.Notifier
	cfg        *model.NotifHomeAssistant
	meta       model.Meta
	mqttClient MQTT.Client
}

// New creates a new mqtt notification instance
func New(config *model.NotifHomeAssistant, meta model.Meta) notifier.Notifier {
	return notifier.Notifier{
		Handler: &Client{
			cfg:  config,
			meta: meta,
		},
	}
}

// Name returns notifier's name
func (c *Client) Name() string {
	return "homeassistant"
}

func getLast8Chars(digest string) string {
	if len(digest) >= 8 {
		return digest[len(digest)-8:]
	}
	return digest
}

// Send creates and sends a mqtt notification with an entry
func (c *Client) Send(entry model.NotifEntry) error {
	username, err := utl.GetSecret(c.cfg.Username, c.cfg.UsernameFile)
	if err != nil {
		return err
	}

	password, err := utl.GetSecret(c.cfg.Password, c.cfg.PasswordFile)
	if err != nil {
		return err
	}

	// Extract the image string
	imageStr := entry.Image.String()
	// Extract the repository name (without version) and sanitize it
	repoName := strings.Split(strings.Split(imageStr, ":")[0], "@")[0]
	parts := strings.Split(repoName, "/")
	extractedName := parts[len(parts)-1]
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	sanitizedImage := re.ReplaceAllString(repoName, "-")

	// Define the topics
	availabilityTopic := fmt.Sprintf("%s/%s/%s/availability", c.cfg.DiscoveryPrefix, c.cfg.Component, c.cfg.NodeName)
	discoveryTopic := fmt.Sprintf("%s/%s/%s/%s/config", c.cfg.DiscoveryPrefix, c.cfg.Component, c.cfg.NodeName, sanitizedImage)
	stateTopic := fmt.Sprintf("%s/%s/%s/%s/state", c.cfg.DiscoveryPrefix, c.cfg.Component, c.cfg.NodeName, sanitizedImage)

	broker := fmt.Sprintf("%s://%s:%d", c.cfg.Scheme, c.cfg.Host, c.cfg.Port)
	opts := MQTT.NewClientOptions().AddBroker(broker).SetClientID(c.cfg.Client).SetWill(availabilityTopic, "offline", byte(c.cfg.QoS), true)
	opts.Username = username
	opts.Password = password

	if c.mqttClient == nil {
		// Create the client
		c.mqttClient = MQTT.NewClient(opts)
		if token := c.mqttClient.Connect(); token.Wait() && token.Error() != nil {
			return token.Error()
		}

		// Create & publish the availability message
		if token := c.mqttClient.Publish(availabilityTopic, byte(c.cfg.QoS), true, "online"); token.Wait() && token.Error() != nil {
			return token.Error()
		}
	}

	// Create & publish the discovery message
	discoveryPayload := map[string]interface{}{
		"state_topic":        stateTopic,
		"name":               extractedName,
		"title":              imageStr,
		"unique_id":          sanitizedImage,
		"availability_topic": availabilityTopic,
		"icon":               "mdi:docker",
		"device": map[string]interface{}{
			"identifiers":  c.cfg.NodeName,
			"name":         c.cfg.NodeName,
			"manufacturer": "DIUN - Docker Image Update Notifier",
		},
	}
	payloadBytes, err := json.Marshal(discoveryPayload)
	if err != nil {
		return err
	}
	if token := c.mqttClient.Publish(discoveryTopic, byte(c.cfg.QoS), true, payloadBytes); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Prepare & the state payload
	latestVersion := getLast8Chars(entry.Manifest.Digest.String())
	var installedVersion string
	if len(entry.PrevManifest.Digest.String()) > 0 {
		installedVersion = getLast8Chars(entry.PrevManifest.Digest.String())
	} else {
		installedVersion = latestVersion
	}
	var statePayload = map[string]interface{}{
		"installed_version": installedVersion,
		"latest_version":    latestVersion,
	}
	statePayloadBytes, err := json.Marshal(statePayload)
	if err != nil {
		return err
	}
	if token := c.mqttClient.Publish(stateTopic, byte(c.cfg.QoS), true, statePayloadBytes); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}
