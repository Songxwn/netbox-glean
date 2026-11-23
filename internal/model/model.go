// Package model contains the shared data types used by the collectors,
// the NetBox writer, the job runner and the HTTP API.
package model

import "time"

// Supported collection protocols.
const (
	ProtocolSNMP    = "snmp"
	ProtocolIPMI    = "ipmi"
	ProtocolRedfish = "redfish"
)

// Job modes.
const (
	ModeCollect = "collect"
	ModeSync    = "sync"
)

// LogFunc is the logging callback shared by collectors and NetBox sync.
type LogFunc func(format string, args ...any)

// Settings holds the global configuration managed through the web UI.
type Settings struct {
	NetboxURL      string `json:"netbox_url"`
	NetboxToken    string `json:"netbox_token,omitempty"`
	NetboxTokenSet bool   `json:"netbox_token_set,omitempty"`
	// TokenVersion accepts "auto", "1" or "2". Auto uses NetBox v2 bearer
	// tokens (nbt_ prefix) when detected, otherwise legacy v1 Token auth.
	TokenVersion    string `json:"token_version"`
	NetboxInsecure  bool   `json:"netbox_insecure"`
	Site            string `json:"site"`
	SiteName        string `json:"site_name,omitempty"`
	DeviceRole      string `json:"device_role"`
	Platform        string `json:"platform,omitempty"`
	UHeight         int    `json:"u_height"`
	SyncInterfaces  bool   `json:"sync_interfaces"`
	SyncInventory   bool   `json:"sync_inventory"`
	SyncCables      bool   `json:"sync_cables"`
	PruneInterfaces bool   `json:"prune_interfaces"`
	CustomFields    bool   `json:"custom_fields"`
	AssignPrimaryIP bool   `json:"assign_primary_ip"`
	PrimaryInterface string `json:"primary_interface"`
	Description     string `json:"description,omitempty"`
}

// DefaultSettings returns sensible defaults for a new installation.
func DefaultSettings() Settings {
	return Settings{
		TokenVersion:     "auto",
		UHeight:          1,
		SyncInterfaces:   true,
		SyncInventory:    true,
		SyncCables:       true,
		CustomFields:     true,
		PrimaryInterface: "mgmt0",
		DeviceRole:       "server",
		Description:      "Managed by netbox-glean",
	}
}

// Target describes one device that can be collected.
type Target struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Protocol    string        `json:"protocol"`
	Address     string        `json:"address"`
	Port        int           `json:"port"`
	Enabled     bool          `json:"enabled"`
	Site        string        `json:"site,omitempty"`
	DeviceRole  string        `json:"device_role,omitempty"`
	Platform    string        `json:"platform,omitempty"`
	DeviceName  string        `json:"device_name,omitempty"`
	Description string        `json:"description,omitempty"`
	SNMP        SNMPConfig    `json:"snmp"`
	IPMI        IPMIConfig    `json:"ipmi"`
	Redfish     RedfishConfig `json:"redfish"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// SNMPConfig holds SNMP v1/v2c/v3 credentials.
type SNMPConfig struct {
	Version        string `json:"version"` // v1, v2c or v3
	Community      string `json:"community,omitempty"`
	V3Username     string `json:"v3_username,omitempty"`
	V3AuthProtocol string `json:"v3_auth_protocol,omitempty"` // MD5, SHA, SHA224, SHA256, SHA384, SHA512
	V3AuthPassword string `json:"v3_auth_password,omitempty"`
	V3PrivProtocol string `json:"v3_priv_protocol,omitempty"` // DES, AES, AES192, AES256
	V3PrivPassword string `json:"v3_priv_password,omitempty"`
}

// IPMIConfig holds IPMI over LAN credentials.
type IPMIConfig struct {
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Interface string `json:"interface,omitempty"` // lanplus (default) or lan
}

// RedfishConfig holds Redfish credentials.
type RedfishConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
	SystemID string `json:"system_id,omitempty"`
}

// Identity is the device-level information written to NetBox.
type Identity struct {
	Name         string  `json:"name"`
	Manufacturer string  `json:"manufacturer"`
	Model        string  `json:"model"`
	Serial       string  `json:"serial"`
	AssetTag     string  `json:"asset_tag"`
	Firmware     string  `json:"firmware"`
	BIOSVersion  string  `json:"bios_version"`
	BMCVersion   string  `json:"bmc_version"`
	PowerState   string  `json:"power_state"`
	CPUModel     string  `json:"cpu_model"`
	CPUCores     int     `json:"cpu_cores"`
	MemoryBytes  int64   `json:"memory_bytes"`
	MemoryGB     float64 `json:"memory_gb"`
	OS           string  `json:"os"`
	UUID         string  `json:"uuid"`
}

// Interface is a normalized network interface.
type Interface struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Alias       string `json:"alias,omitempty"`
	Type        string `json:"type,omitempty"`
	MAC         string `json:"mac,omitempty"`
	MTU         int    `json:"mtu,omitempty"`
	SpeedMbps   int    `json:"speed_mbps,omitempty"`
	AdminUp     bool   `json:"admin_up"`
	OperUp      bool   `json:"oper_up"`
}

// Neighbor is a link layer neighbor learned through LLDP. It is used to
// create cables automatically in NetBox.
type Neighbor struct {
	LocalInterface  string `json:"local_interface"`
	LocalPortID     string `json:"local_port_id,omitempty"`
	RemoteSystem    string `json:"remote_system,omitempty"`
	RemotePort      string `json:"remote_port,omitempty"`
	RemotePortDesc  string `json:"remote_port_desc,omitempty"`
	RemoteChassisID string `json:"remote_chassis_id,omitempty"`
	RemoteSysDesc   string `json:"remote_sys_desc,omitempty"`
}

// Component is a hardware component (inventory item in NetBox).
type Component struct {
	Name         string `json:"name"`
	Class        string `json:"class,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	AssetTag     string `json:"asset_tag,omitempty"`
	PartNumber   string `json:"part_number,omitempty"`
	Description  string `json:"description,omitempty"`
	Firmware     string `json:"firmware,omitempty"`
	Parent       string `json:"parent,omitempty"`
}

// Collected is the normalized output of one collection run.
type Collected struct {
	Source      string            `json:"source"`
	CollectedAt time.Time         `json:"collected_at"`
	Identity    Identity          `json:"identity"`
	Interfaces  []Interface       `json:"interfaces,omitempty"`
	Components  []Component       `json:"components,omitempty"`
	Neighbors   []Neighbor        `json:"neighbors,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
}

// SyncResult reports what was changed in NetBox.
type SyncResult struct {
	DeviceID          int      `json:"device_id"`
	DeviceName        string   `json:"device_name"`
	DeviceURL         string   `json:"device_url"`
	Created           bool     `json:"created"`
	InterfacesCreated int      `json:"interfaces_created"`
	InterfacesUpdated int      `json:"interfaces_updated"`
	InventoryCreated  int      `json:"inventory_created"`
	InventoryUpdated  int      `json:"inventory_updated"`
	CablesCreated     int      `json:"cables_created"`
	CablesSkipped     int      `json:"cables_skipped"`
	IPAddress         string   `json:"ip_address,omitempty"`
	Actions           []string `json:"actions,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// Add appends a human readable action line.
func (r *SyncResult) Add(format string, args ...any) {
	r.Actions = append(r.Actions, Sprintf(format, args...))
}

// Warn appends a non-fatal warning.
func (r *SyncResult) Warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, Sprintf(format, args...))
}