// Copyright 2026. Licensed under the BSD-3-Clause license, matching upstream
// naiveproxy.
//
// naivereal_h3_server: a QUICHE HTTP/3 origin that serves an operator-owned
// website and terminates naiveproxy CONNECT tunnels on the same UDP endpoint,
// using Chrome's own QUIC implementation for the server role.
//
// It replaces the Go h3frontend's transport so that the server half of the
// connection advertises QUICHE's transport-parameter shape rather than
// quic-go's. The transport is the only thing that changes; the naive protocol
// contract, the website routing and the per-request authorization model are
// deliberately identical.
//
// Usage:
//   naivereal_h3_server --port=443 \
//     --certificate_file=/etc/naivereal/fullchain.pem \
//     --key_file=/etc/naivereal/privkey.pem \
//     --web_root=/var/www/naivereal \
//     --username=user --password=pass \
//     --upstream_addr=127.0.0.1:18080

#include <memory>
#include <string>
#include <vector>

#include "naive_server_backend.h"
#include "common/platform/api/quiche_command_line_flags.h"
#include "common/platform/api/quiche_system_event_loop.h"
#include "quic/tools/quic_server_factory.h"
#include "quic/tools/quic_toy_server.h"

ABSL_FLAG(std::string, web_root, "", "Directory served for non-CONNECT GET/HEAD requests.");
ABSL_FLAG(std::string, username, "", "Expected HTTP Basic username for CONNECT.");
ABSL_FLAG(std::string, password, "", "Expected HTTP Basic password for CONNECT.");
ABSL_FLAG(std::string, upstream_addr, "",
          "Forward CONNECT tunnels to this address (an official naive HTTP "
          "server). Empty dials the request authority directly.");
ABSL_FLAG(std::string, server_label, "naivereal-h3",
          "Identifier used in error responses, per RFC 9209 section 2.");

// Congestion-control tuning.
//
// These map onto QUICHE's protocol flags, which QUICHE defines as plain globals
// and does not register with the command-line parser, so they are surfaced here
// and assigned before the server starts.
//
// Tuning them is not a fingerprint: BBR's congestion window gain and pacing
// behaviour are sender-local dynamics, applied after the handshake. They change
// no transport parameter, no frame and no packet layout, so a server-library
// classifier sees exactly the same QUICHE shape either way. What they do change
// is the timing profile, which matters only to statistical size/timing analysis.
ABSL_FLAG(double, bbr_cwnd_gain, 2.0,
          "BBR congestion window gain during PROBE_BW. QUICHE default 2.0.");
ABSL_FLAG(int32_t, max_congestion_window, 2000,
          "Maximum congestion window, in packets. QUICHE default 2000.");
ABSL_FLAG(int32_t, lumpy_pacing_size, 2,
          "Packets per pacing burst when lumpy pacing is active. Default 2.");
ABSL_FLAG(double, lumpy_pacing_cwnd_fraction, 0.25,
          "Congestion window fraction used as the lumpy pacing burst. Default 0.25.");

// QUICHE protocol flags that this binary sets directly. Declared here because
// QUICHE defines them in its own translation unit.
extern double FLAGS_quic_bbr_cwnd_gain;
extern int32_t FLAGS_quic_max_congestion_window;
extern int32_t FLAGS_quic_lumpy_pacing_size;
extern double FLAGS_quic_lumpy_pacing_cwnd_fraction;

namespace {

// Applies the congestion-control flags above to QUICHE's globals.
void ApplyCongestionControlFlags() {
  FLAGS_quic_bbr_cwnd_gain =
      quiche::GetQuicheCommandLineFlag(FLAGS_bbr_cwnd_gain);
  FLAGS_quic_max_congestion_window =
      quiche::GetQuicheCommandLineFlag(FLAGS_max_congestion_window);
  FLAGS_quic_lumpy_pacing_size =
      quiche::GetQuicheCommandLineFlag(FLAGS_lumpy_pacing_size);
  FLAGS_quic_lumpy_pacing_cwnd_fraction =
      quiche::GetQuicheCommandLineFlag(FLAGS_lumpy_pacing_cwnd_fraction);
}

}  // namespace

namespace {

// Hands the toy server a naive backend instead of its in-memory cache.
class NaiveBackendFactory : public quic::QuicToyServer::BackendFactory {
 public:
  std::unique_ptr<quic::QuicSimpleServerBackend> CreateBackend() override {
    naivereal::NaiveBackendConfig config;
    config.web_root = quiche::GetQuicheCommandLineFlag(FLAGS_web_root);
    config.username = quiche::GetQuicheCommandLineFlag(FLAGS_username);
    config.password = quiche::GetQuicheCommandLineFlag(FLAGS_password);
    config.upstream_addr = quiche::GetQuicheCommandLineFlag(FLAGS_upstream_addr);
    config.server_label = quiche::GetQuicheCommandLineFlag(FLAGS_server_label);
    return std::make_unique<naivereal::NaiveServerBackend>(std::move(config));
  }
};

}  // namespace

int main(int argc, char* argv[]) {
  quiche::QuicheSystemEventLoop event_loop("naivereal_h3_server");
  const char* usage =
      "Usage: naivereal_h3_server [options]\n"
      "\n"
      "Serves a website and naiveproxy CONNECT tunnels over HTTP/3.\n"
      "The certificate and key are read from --certificate_file and --key_file.";
  std::vector<std::string> non_option_args =
      quiche::QuicheParseCommandLineFlags(usage, argc, argv);
  if (!non_option_args.empty()) {
    quiche::QuichePrintCommandLineFlagHelp(usage);
    return 0;
  }

  ApplyCongestionControlFlags();

  NaiveBackendFactory backend_factory;
  quic::QuicServerFactory server_factory;
  quic::QuicToyServer server(&backend_factory, &server_factory);
  return server.Start();
}
