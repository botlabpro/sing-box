package outbound

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/mux"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2ray"
	"github.com/sagernet/sing-box/transport/vless"
	"github.com/sagernet/sing-vmess/packetaddr"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"strings"
)

var _ adapter.Outbound = (*VLESS)(nil)

type VLESSVPPL struct {
	Enabled bool
	Proxy   bool
	Address M.Socksaddr
	Key     *rsa.PublicKey
}
type VLESS struct {
	myOutboundAdapter
	dialer          N.Dialer
	client          *vless.Client
	serverAddr      M.Socksaddr
	multiplexDialer *mux.Client
	tlsConfig       tls.Config
	transport       adapter.V2RayClientTransport
	packetAddr      bool
	xudp            bool
	vppl            VLESSVPPL
	originDest      []byte
}

func NewVLESS(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.VLESSOutboundOptions) (*VLESS, error) {
	outboundDialer, err := dialer.New(router, options.DialerOptions)
	if err != nil {
		return nil, err
	}

	outbound := &VLESS{
		myOutboundAdapter: myOutboundAdapter{
			protocol:     C.TypeVLESS,
			network:      options.Network.Build(),
			router:       router,
			logger:       logger,
			tag:          tag,
			dependencies: withDialerDependency(options.DialerOptions),
		},
		dialer:     outboundDialer,
		serverAddr: options.VLESSServerOptions.Build(),
	}

	if options.VPPL.Enabled {
		outbound.vppl = VLESSVPPL{
			Enabled: true,
			Proxy:   options.VPPL.Proxy,
		}

		if !options.VPPL.Proxy {
			if len(options.VPPL.Key) == 0 {
				return nil, errors.New("VPPL: no public key")
			}
			r := strings.NewReader(fmt.Sprintf("-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----", options.VPPL.Key))
			pemBytes, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}

			publicKeyBlock, _ := pem.Decode(pemBytes)

			publicKey, err := x509.ParsePKIXPublicKey(publicKeyBlock.Bytes)
			if err != nil {
				return nil, err
			}

			outbound.vppl.Key = publicKey.(*rsa.PublicKey)
			outbound.vppl.Address = M.ParseSocksaddrHostPort(options.VPPL.Host, uint16(options.VPPL.Port))
		}
	}

	if options.TLS != nil {
		outbound.tlsConfig, err = tls.NewClient(ctx, options.Server, common.PtrValueOrDefault(options.TLS))
		if err != nil {
			return nil, err
		}
	}

	if options.Transport != nil {
		if options.VPPL.Enabled {
			return nil, E.New("VPPL does not support transport options")
		}

		outbound.transport, err = v2ray.NewClientTransport(ctx, outbound.dialer, outbound.serverAddr, common.PtrValueOrDefault(options.Transport), outbound.tlsConfig)
		if err != nil {
			return nil, E.Cause(err, "create client transport: ", options.Transport.Type)
		}
	}

	if options.PacketEncoding == nil {
		outbound.xudp = true
	} else {
		switch *options.PacketEncoding {
		case "":
		case "packetaddr":
			outbound.packetAddr = true
		case "xudp":
			outbound.xudp = true
		default:
			return nil, E.New("unknown packet encoding: ", options.PacketEncoding)
		}
	}

	outbound.client, err = vless.NewClient(options.UUID, options.Flow, logger)
	if err != nil {
		return nil, err
	}

	outbound.multiplexDialer, err = mux.NewClientWithOptions((*vlessDialer)(outbound), logger, common.PtrValueOrDefault(options.Multiplex))
	if err != nil {
		return nil, err
	}

	return outbound, nil
}

func (h *VLESS) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if h.vppl.Enabled {
		if !h.vppl.Proxy {
			h.originDest = []byte(destination.String())
			encDest, err := rsa.EncryptPKCS1v15(rand.Reader, h.vppl.Key, []byte(destination.String()))
			if err != nil {
				return nil, err
			}

			h.originDest = encDest
			destination = h.vppl.Address
		} else {
			inbCtx := adapter.ContextFrom(ctx)
			if len(inbCtx.VPPLdestination) == 0 {
				return nil, E.New("VPPL destination is empty with mode proxy")
			}

			h.originDest = inbCtx.VPPLdestination
		}
	}

	if h.multiplexDialer == nil {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			h.logger.InfoContext(ctx, "outbound connection to ", destination)
		case N.NetworkUDP:
			h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		}
		return (*vlessDialer)(h).DialContext(ctx, network, destination)
	} else {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			h.logger.InfoContext(ctx, "outbound multiplex connection to ", destination)
		case N.NetworkUDP:
			h.logger.InfoContext(ctx, "outbound multiplex packet connection to ", destination)
		}
		return h.multiplexDialer.DialContext(ctx, network, destination)
	}
}

func (h *VLESS) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.multiplexDialer == nil {
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		return (*vlessDialer)(h).ListenPacket(ctx, destination)
	} else {
		h.logger.InfoContext(ctx, "outbound multiplex packet connection to ", destination)
		return h.multiplexDialer.ListenPacket(ctx, destination)
	}
}

func (h *VLESS) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	return NewConnection(ctx, h, conn, metadata)
}

func (h *VLESS) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext) error {
	return NewPacketConnection(ctx, h, conn, metadata)
}

func (h *VLESS) InterfaceUpdated() {
	if h.multiplexDialer != nil {
		h.multiplexDialer.Reset()
	}
}

func (h *VLESS) Close() error {
	return common.Close(common.PtrOrNil(h.multiplexDialer), h.transport)
}

type vlessDialer VLESS

func (h *vlessDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.AppendContext(ctx)
	metadata.Outbound = h.tag
	metadata.Destination = destination
	var conn net.Conn
	var err error
	if h.transport != nil {
		conn, err = h.transport.DialContext(ctx)
	} else {
		server := h.serverAddr
		if h.vppl.Enabled && h.vppl.Proxy {
			server = destination
		}

		conn, err = h.dialer.DialContext(ctx, N.NetworkTCP, server)
		if err == nil && h.tlsConfig != nil {
			h.logger.InfoContext(ctx, "outbound connection handshake ", conn.RemoteAddr())
			conn, err = tls.ClientHandshake(ctx, conn, h.tlsConfig)
			h.logger.InfoContext(ctx, "outbound connection handshake error ", err)
		}
	}
	if err != nil {
		return nil, err
	}
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.client.DialEarlyConn(conn, destination, h.originDest)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		if h.xudp {
			return h.client.DialEarlyXUDPPacketConn(conn, destination, h.originDest)
		} else if h.packetAddr {
			if destination.IsFqdn() {
				return nil, E.New("packetaddr: domain destination is not supported")
			}
			packetConn, err := h.client.DialEarlyPacketConn(conn, M.Socksaddr{Fqdn: packetaddr.SeqPacketMagicAddress})
			if err != nil {
				return nil, err
			}
			return bufio.NewBindPacketConn(packetaddr.NewConn(packetConn, destination), destination), nil
		} else {
			return h.client.DialEarlyPacketConn(conn, destination)
		}
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *vlessDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	ctx, metadata := adapter.AppendContext(ctx)
	metadata.Outbound = h.tag
	metadata.Destination = destination
	var conn net.Conn
	var err error
	if h.transport != nil {
		conn, err = h.transport.DialContext(ctx)
	} else {
		server := h.serverAddr
		if h.vppl.Enabled && h.vppl.Proxy {
			server = destination
		}

		conn, err = h.dialer.DialContext(ctx, N.NetworkTCP, server)
		if err == nil && h.tlsConfig != nil {
			conn, err = tls.ClientHandshake(ctx, conn, h.tlsConfig)
		}
	}
	if err != nil {
		common.Close(conn)
		return nil, err
	}
	if h.xudp {
		return h.client.DialEarlyXUDPPacketConn(conn, destination, h.originDest)
	} else if h.packetAddr {
		if destination.IsFqdn() {
			return nil, E.New("packetaddr: domain destination is not supported")
		}
		conn, err := h.client.DialEarlyPacketConn(conn, M.Socksaddr{Fqdn: packetaddr.SeqPacketMagicAddress})
		if err != nil {
			return nil, err
		}
		return packetaddr.NewConn(conn, destination), nil
	} else {
		return h.client.DialEarlyPacketConn(conn, destination)
	}
}
