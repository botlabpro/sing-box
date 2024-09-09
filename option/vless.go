package option

import M "github.com/sagernet/sing/common/metadata"

type VPPLOptions struct {
	Enabled   bool   `json:"enabled"`
	PathToKey string `json:"path_to_key,omitempty"`
	Key       string `json:"key,omitempty"`
	Proxy     bool   `json:"proxy,omitempty"`
}

type VPPLOutboundOptions struct {
	VPPLOptions
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

type VLESSInboundOptions struct {
	ListenOptions
	Users []VLESSUser `json:"users,omitempty"`
	InboundTLSOptionsContainer
	Multiplex *InboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport *V2RayTransportOptions   `json:"transport,omitempty"`
	VPPL      VPPLOptions              `json:"vppl,omitempty"`
}

type VLESSUser struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	Flow string `json:"flow,omitempty"`
}

type VLESSServerOptions struct {
	Server     string `json:"server,omitempty"`
	ServerPort uint16 `json:"server_port,omitempty"`
}

func (o VLESSServerOptions) Build() M.Socksaddr {
	return M.ParseSocksaddrHostPort(o.Server, o.ServerPort)
}

func (o *VLESSServerOptions) TakeServerOptions() VLESSServerOptions {
	return *o
}

func (o *VLESSServerOptions) ReplaceServerOptions(options VLESSServerOptions) {
	*o = options
}

type VLESSOutboundOptions struct {
	DialerOptions
	VLESSServerOptions
	UUID    string      `json:"uuid"`
	Flow    string      `json:"flow,omitempty"`
	Network NetworkList `json:"network,omitempty"`
	OutboundTLSOptionsContainer
	Multiplex      *OutboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport      *V2RayTransportOptions    `json:"transport,omitempty"`
	PacketEncoding *string                   `json:"packet_encoding,omitempty"`
	VPPL           VPPLOutboundOptions       `json:"vppl,omitempty"`
}
