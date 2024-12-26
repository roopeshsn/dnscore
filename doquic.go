//
// SPDX-License-Identifier: GPL-3.0-or-later
//
// DNS-over-QUIC implementation
//

// DNS over Dedicated QUIC Connections
// RFC 9250
// https://datatracker.ietf.org/doc/rfc9250/

package dnscore

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
)

func (t *Transport) sendQueryQUIC(ctx context.Context, addr *ServerAddr,
	query *dns.Msg) (stream quic.Stream, t0 time.Time, rawQuery []byte, err error) {

	udpAddr, err := net.ResolveUDPAddr("udp", addr.Address)
	if err != nil {
		return
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return
	}

	tr := &quic.Transport{
		Conn: udpConn,
	}

	// 1. Fill in a default TLS config and QUIC config
	hostname, _, err := net.SplitHostPort(addr.Address)
	if err != nil {
		return
	}
	tlsConfig := &tls.Config{
		NextProtos: []string{"doq"},
		ServerName: hostname,
	}
	quicConfig := &quic.Config{}

	// 2. Use the context deadline to limit the query lifetime
	// as documented in the [*Transport.Query] function.
	if deadline, ok := ctx.Deadline(); ok {
		_ = udpConn.SetDeadline(deadline)
	}

	// RFC 9250
	// 4.2.1.  DNS Message IDs
	// When sending queries over a QUIC connection, the DNS Message ID MUST
	// be set to 0.
	query.Id = 0
	rawQuery, err = query.Pack()
	if err != nil {
		return
	}

	t0 = t.maybeLogQuery(ctx, addr, rawQuery)

	quicConn, err := tr.Dial(ctx, udpAddr, tlsConfig, quicConfig)
	if err != nil {
		return
	}

	stream, err = quicConn.OpenStream()
	if err != nil {
		return
	}
	stream.Write(rawQuery)

	// RFC 9250
	// 4.2.  Stream Mapping and Usage
	// The client MUST send the DNS query over the selected stream and MUST
	// indicate through the STREAM FIN mechanism that no further data will
	// be sent on that stream.
	_ = stream.Close()

	return
}

// recvResponseUDP reads and parses the response from the server and
// possibly logs the response. It returns the parsed response or an error.
func (t *Transport) recvResponseQUIC(ctx context.Context, addr *ServerAddr, stream quic.Stream,
	t0 time.Time, query *dns.Msg, rawQuery []byte) (*dns.Msg, error) {
	// 1. Read the corresponding raw response
	buffer := make([]byte, 1024)
	io.ReadFull(stream, buffer)

	// 2. Parse the raw response and possibly log that we received it.
	resp := &dns.Msg{}
	if err := resp.Unpack(buffer); err != nil {
		return nil, err
	}

	// t.maybeLogResponseConn(ctx, addr, t0, rawQuery, buffer, conn)

	return resp, nil
}

func (t *Transport) queryQUIC(ctx context.Context, addr *ServerAddr, query *dns.Msg) (*dns.Msg, error) {
	// 0. immediately fail if the context is already done, which
	// is useful to write unit tests
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Send the query and log the query if needed.
	stream, t0, rawQuery, err := t.sendQueryQUIC(ctx, addr, query)
	if err != nil {
		return nil, err
	}

	// ctx, cancel := context.WithCancel(ctx)
	// defer cancel()
	// go func() {
	// 	defer stream.Close()
	// 	<-ctx.Done()
	// }()

	// Read and parse the response and log it if needed.
	return t.recvResponseQUIC(ctx, addr, stream, t0, query, rawQuery)
}
