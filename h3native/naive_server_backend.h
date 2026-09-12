// Copyright 2026. Licensed under the BSD-3-Clause license, matching upstream
// naiveproxy.
//
// A QUICHE QuicSimpleServerBackend that speaks the naive proxy protocol, so a
// QUICHE (Chrome) HTTP/3 server can terminate naiveproxy CONNECT tunnels and
// serve an operator-owned website on the same endpoint.
//
// This exists to remove the Go implementation's server-library fingerprint:
// quic-go advertises a transport-parameter set and order that server-library
// classifiers score as quic-go, while QUICHE advertises its own. Only the
// transport is replaced here; the naive protocol contract is unchanged.
//
// Protocol contract (mirrors klzgrad/forwardproxy and h3frontend):
//   - Non-CONNECT requests are served from the website root (GET/HEAD only).
//   - CONNECT is tunnelled only when Proxy-Authorization: Basic matches, per
//     request; there is no per-connection or per-address authorization cache.
//   - Unauthenticated CONNECT is served as a website request, so the endpoint
//     does not reveal that a proxy exists.
//   - The tunnel payload uses the naive padding frame for the first
//     kFirstPaddings frames in each direction: a 3-byte header (uint16 payload
//     size big-endian, uint8 padding size) followed by payload then zero
//     padding. Padding is added when writing to the client and removed when
//     reading from it.

#ifndef NAIVEREAL_H3_NAIVE_SERVER_BACKEND_H_
#define NAIVEREAL_H3_NAIVE_SERVER_BACKEND_H_

#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "absl/container/flat_hash_map.h"
#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "quic/core/connecting_client_socket.h"
#include "quic/core/quic_types.h"
#include "quic/core/socket_factory.h"
#include "quic/tools/quic_simple_server_backend.h"
#include "common/http/http_header_block.h"
#include "common/quiche_mem_slice.h"

namespace naivereal {

// Configuration for the naive backend. Paths are read once at startup.
struct NaiveBackendConfig {
  // Directory served for non-CONNECT requests. Empty disables the website.
  std::string web_root;
  // Expected HTTP Basic credentials. Both must be non-empty to accept CONNECT.
  std::string username;
  std::string password;
  // Upstream is the target for CONNECT. When set, the tunnel forwards to this
  // single address (an official naive HTTP server) instead of dialling the
  // request authority directly. This keeps the padding/forward-proxy logic in
  // the upstream, matching the h3frontend deployment.
  std::string upstream_addr;
  // Random label used in error responses, per RFC 9209.
  std::string server_label;
};

// NaivePaddingFramer applies and removes the naive padding frame.
//
// The client pads the first kFirstPaddings writes to the server and the server
// pads the first kFirstPaddings writes back, so both directions are framed
// independently.
class NaivePaddingFramer {
 public:
  static constexpr int kFirstPaddings = 8;
  static constexpr int kMaxPaddingSize = 255;

  // Removes padding from bytes read from the peer. Returns the unpadded payload
  // for this call; bytes that do not yet form a complete frame are retained
  // internally.
  std::string RemovePadding(absl::string_view data);

  // Wraps payload in a padding frame. Only the first kFirstPaddings calls add
  // padding; later calls pass through unchanged.
  std::string AddPadding(absl::string_view payload);

  int frames_seen() const { return frames_seen_; }

 private:
  bool remove_done_ = false;
  bool add_done_ = false;
  int frames_seen_ = 0;
  // Partial frame carried between RemovePadding calls.
  std::string pending_;
};

class NaiveServerBackend : public quic::QuicSimpleServerBackend {
 public:
  explicit NaiveServerBackend(NaiveBackendConfig config);
  ~NaiveServerBackend() override;

  NaiveServerBackend(const NaiveServerBackend&) = delete;
  NaiveServerBackend& operator=(const NaiveServerBackend&) = delete;

  // quic::QuicSimpleServerBackend:
  bool InitializeBackend(const std::string& backend_url) override;
  bool IsBackendInitialized() const override { return initialized_; }
  void SetSocketFactory(quic::SocketFactory* socket_factory) override;
  void FetchResponseFromBackend(
      const quiche::HttpHeaderBlock& request_headers,
      const std::string& request_body,
      RequestHandler* request_handler) override;
  void HandleConnectHeaders(const quiche::HttpHeaderBlock& request_headers,
                            RequestHandler* request_handler) override;
  void HandleConnectData(absl::string_view data, bool data_complete,
                         RequestHandler* request_handler) override;
  void CloseBackendResponseStream(RequestHandler* request_handler) override;

  // Number of CONNECT tunnels currently open. Test hook.
  size_t active_tunnels() const;

 private:
  class Tunnel;

  // Serves `path` from the website root. Returns false when the request must
  // not be served as a website request.
  bool ServeWebsite(const quiche::HttpHeaderBlock& request_headers,
                    RequestHandler* request_handler);

  // Returns true when the request carries valid Basic credentials.
  bool Authorized(const quiche::HttpHeaderBlock& request_headers) const;

  // Builds the response header block for a served file.
  bool ReadFile(absl::string_view path, std::string* content,
                std::string* content_type) const;

  NaiveBackendConfig config_;
  bool initialized_ = false;
  quic::SocketFactory* socket_factory_ = nullptr;
  // Owns the sockets handed to tunnels. Created on SetSocketFactory so
  // it can bind to the server's event loop.
  std::unique_ptr<quic::SocketFactory> tunnel_socket_factory_;
  // Live tunnels, keyed by the request handler that owns the CONNECT stream.
  // Keying (rather than scanning) keeps HandleConnectData bound to the right
  // tunnel when several CONNECT streams share one QUIC connection.
  absl::flat_hash_map<RequestHandler*, std::unique_ptr<Tunnel>> tunnels_;
};

}  // namespace naivereal

#endif  // NAIVEREAL_H3_NAIVE_SERVER_BACKEND_H_
