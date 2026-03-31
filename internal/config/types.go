package config

import "time"

const CurrentVersion = 1
const InstanceKindDaemon = "daemon"

type YoloMode string

const (
	YoloModeOff    YoloMode = "off"
	YoloModeBypass YoloMode = "bypass"
	YoloModeRules  YoloMode = "rules"
)

type Config struct {
	Version       int            `json:"version"`
	ProxyEnabled  *bool          `json:"proxyEnabled,omitempty"`
	YoloEnabled   *bool          `json:"yoloEnabled,omitempty"`
	YoloMode      *string        `json:"yoloMode,omitempty"`
	Profiles      []Profile      `json:"profiles"`
	Instances     []Instance     `json:"instances"`
	PatchFailures []PatchFailure `json:"patchFailures,omitempty"`
}

type PatchFailure struct {
	ProxyVersion  string    `json:"proxyVersion"`
	HostID        string    `json:"hostId,omitempty"`
	ClaudeVersion string    `json:"claudeVersion,omitempty"`
	ClaudePath    string    `json:"claudePath,omitempty"`
	ClaudeSHA256  string    `json:"claudeSha256,omitempty"`
	FailedAt      time.Time `json:"failedAt"`
	Reason        string    `json:"reason,omitempty"`
}

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	SSHArgs   []string  `json:"sshArgs,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Instance struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profileId"`
	Kind       string    `json:"kind,omitempty"`
	HTTPPort   int       `json:"httpPort"`
	SocksPort  int       `json:"socksPort"`
	DaemonPID  int       `json:"daemonPid"`
	StartedAt  time.Time `json:"startedAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}
