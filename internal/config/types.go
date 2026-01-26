package config

import "time"

const CurrentVersion = 1

type Config struct {
	Version      int        `json:"version"`
	ProxyEnabled *bool      `json:"proxyEnabled,omitempty"`
	YoloEnabled  *bool      `json:"yoloEnabled,omitempty"`
	Profiles     []Profile  `json:"profiles"`
	Instances    []Instance `json:"instances"`
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
	HTTPPort   int       `json:"httpPort"`
	SocksPort  int       `json:"socksPort"`
	DaemonPID  int       `json:"daemonPid"`
	StartedAt  time.Time `json:"startedAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}
