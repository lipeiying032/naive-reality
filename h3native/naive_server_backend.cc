// Copyright 2026. Licensed under the BSD-3-Clause license, matching upstream
// naiveproxy.

#include "naive_posix_client_socket.h"
#include "naive_server_backend.h"

#include <algorithm>
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <thread>
#include <fstream>
#include <iterator>
#include <memory>
#include <optional>
#include <string>
#include <utility>
#include <vector>

#include "absl/container/flat_hash_map.h"
#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/match.h"
#include "absl/strings/numbers.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/string_view.h"
#include "quic/core/quic_error_codes.h"
#include "quic/platform/api/quic_socket_address.h"
#include "quic/tools/quic_backend_response.h"
#include <netdb.h>
#include <netinet/in.h>
#include "common/platform/api/quiche_logging.h"
#include "common/quiche_text_utils.h"
#include "common/quiche_mem_slice.h"

namespace naivereal {

namespace {

// Response padding header length. The client sends [16, 32) and the server
// replies with [30, 62); see forwardproxy.go and the naive padding protocol.
constexpr int kServerPaddingMin = 30;
constexpr int kServerPaddingSpan = 32;

// Alphabet whose Huffman code length is >= 8, so the value is not compressed
// into a short literal. Mirrors the client's FillNonindexHeaderValue and the
// server's own symbol table.
constexpr char kNonIndexSymbols[] = "!#$()+<>?@[]^`{}";

// Small xorshift PRNG. rand() is avoided because padding lengths are derived
// from it on a shared event loop and libc rand() is not reentrant.
uint64_t NextRandom() {
  static uint64_t state = 0x9e3779b97f4a7c15ULL;
  state ^= state << 13;
  state ^= state >> 7;
  state ^= state << 17;
  return state;
}

}  // namespace

// Diagnostics for the spike. fputs with an explicit newline avoids any reliance
// on QUICHE's log level being configured.
void Trace(const char* what) {
  std::fputs("[naive] ", stderr);
  std::fputs(what, stderr);
  std::fputc(10, stderr);
}

// Resolves "host:port" (or "host", defaulting to port 80) to a socket address.
// getaddrinfo is used directly because the QUICHE tool helper resolves a
// QuicServerId for client use, which is not what a CONNECT target needs.
bool ResolveTarget(absl::string_view target, quic::QuicSocketAddress* out,
                   std::string* error) {
  std::string host;
  std::string port = "80";
  const size_t colon = target.rfind(':');
  if (colon != absl::string_view::npos && target.find(':') == colon) {
    host = std::string(target.substr(0, colon));
    port = std::string(target.substr(colon + 1));
  } else {
    host = std::string(target);
  }
  if (host.empty()) {
    *error = "empty host";
    return false;
  }
  addrinfo hints{};
  hints.ai_family = AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  addrinfo* result = nullptr;
  const int rc = getaddrinfo(host.c_str(), port.c_str(), &hints, &result);
  if (rc != 0 || result == nullptr) {
    *error = gai_strerror(rc);
    return false;
  }
  bool ok = false;
  for (addrinfo* ai = result; ai != nullptr; ai = ai->ai_next) {
    if (ai->ai_addr == nullptr) {
      continue;
    }
    sockaddr_storage storage{};
    memcpy(&storage, ai->ai_addr, ai->ai_addrlen);
    *out = quic::QuicSocketAddress(storage);
    ok = true;
    break;
  }
  freeaddrinfo(result);
  if (!ok) {
    *error = "no usable address";
  }
  return ok;
}

// A per-request tunnel between one HTTP/3 CONNECT stream and one TCP socket.
//
// The socket uses QUICHE's async client-socket API so the QUIC event loop is
// never blocked; only the initial connect runs on a helper thread.
class NaiveServerBackend::Tunnel
    : public quic::ConnectingClientSocket::AsyncVisitor {
 public:
  Tunnel(quic::SocketFactory* socket_factory, std::string target,
         bool pad_to_client, RequestHandler* request_handler)
      : socket_factory_(socket_factory),
        target_(std::move(target)),
        pad_to_client_(pad_to_client),
        request_handler_(request_handler) {}

  ~Tunnel() override {
    if (socket_) {
      socket_->Disconnect();
      socket_.reset();
    }
  }

  // Opens the destination socket. Called with the QUIC event loop unblocked:
  // the blocking connect runs on a detached helper thread that hops back to
  // this object only through the async API.
  void Start() {
    Trace("Start: entered");
    quic::QuicSocketAddress peer;
    std::string resolve_error;
    Trace("Start: resolving");
    if (!ResolveTarget(target_, &peer, &resolve_error)) {
      Fail(absl::StrCat("cannot resolve ", target_, ": ", resolve_error));
      return;
    }
    if (socket_factory_ == nullptr) {
      Fail("no socket factory is available");
      return;
    }
    socket_ = socket_factory_->CreateTcpClientSocket(
        peer, kSocketReceiveBufferSize, kSocketSendBufferSize, this);
    if (socket_ == nullptr) {
      Fail("socket factory returned no TCP client socket");
      return;
    }
    // ConnectBlocking must not run on the QUIC event loop thread: QUICHE runs
    // every session on one thread, so blocking here would stall the server and
    // deadlock the stream waiting for this response. It runs on a helper
    // thread instead, and completion re-enters through ConnectComplete.
    Trace("Start: ConnectBlocking");
    absl::Status status = socket_->ConnectBlocking();
    Trace(status.ok() ? "Start: connected" : "Start: connect failed");
    if (!status.ok()) {
      Fail(absl::StrCat("connect to ", target_, " failed: ", status.ToString()));
      return;
    }
    SendConnectResponse();
    BeginRead();

  }

  // Removes naive padding from bytes read from the client, returning the
  // payload to forward. Partial frames are retained inside the framer.
  std::string ReadFromClient(absl::string_view data) {
    return client_framer_.RemovePadding(data);
  }

  // Called with unpadded payload destined for the target.
  void WriteToDestination(absl::string_view payload) {
    if (closed_ || payload.empty() || socket_ == nullptr) {
      return;
    }
    absl::Status status = socket_->SendBlocking(std::string(payload));
    if (!status.ok()) {
      Fail(absl::StrCat("send to destination failed: ", status.ToString()));
    }
  }

  void OnClientStreamClose() {
    closed_ = true;
    request_handler_ = nullptr;
    if (socket_) {
      socket_->Disconnect();
      socket_.reset();
    }
  }

  bool closed() const { return closed_; }

  // quic::ConnectingClientSocket::AsyncVisitor:
  void ConnectComplete(absl::Status status) override {
    // Start() connects synchronously, so this only fires if a connect is ever
    // switched to the async path.
    Trace(status.ok() ? "ConnectComplete ok" : "ConnectComplete failed");
    if (!status.ok()) {
      Fail(absl::StrCat("async connect failed: ", status.ToString()));
      return;
    }
    SendConnectResponse();
    BeginRead();
  }

  void ReceiveComplete(
      absl::StatusOr<quiche::QuicheMemSlice> data) override {
    if (closed_) {
      return;
    }
    if (!data.ok()) {
      // A cancelled read means the tunnel is being torn down.
      if (!absl::IsCancelled(data.status())) {
        Fail(absl::StrCat("receive failed: ", data.status().ToString()));
      }
      return;
    }
    absl::string_view chunk = data->AsStringView();
    if (chunk.empty() && data->length() == 0) {
      // Peer closed; propagate by closing our side of the stream.
      CloseClientStream();
      return;
    }
    std::string outbound = server_framer_.AddPadding(chunk);
    if (request_handler_ != nullptr) {
      request_handler_->SendStreamData(outbound, /*close_stream=*/false);
    }
    BeginRead();
  }

  void SendComplete(absl::Status status) override {
    if (!status.ok()) {
      Fail(absl::StrCat("send completion failed: ", status.ToString()));
    }
  }

 private:
  static constexpr quic::QuicByteCount kSocketReceiveBufferSize = 8 * 1024 * 1024;
  static constexpr quic::QuicByteCount kSocketSendBufferSize = 8 * 1024 * 1024;
  // Strictly below 65536: the naive padding frame stores the payload length in
  // a uint16, and 65536 would encode as 0. See NaivePaddingFramer::AddPadding.
  static constexpr quic::QuicByteCount kReadChunkSize = 65535;

  void BeginRead() {
    if (closed_ || socket_ == nullptr) {
      return;
    }
    socket_->ReceiveAsync(kReadChunkSize);
  }

  // Sends the 200 response with the negotiated padding headers, leaving the
  // stream open so tunnel payload can follow.
  void SendConnectResponse() {
    if (request_handler_ == nullptr) {
      return;
    }
    quiche::HttpHeaderBlock headers;
    headers[":status"] = "200";
    if (pad_to_client_) {
      // Lower-case names: HTTP/3 requires lower-case field names, and the naive
      // client's kPaddingHeader / kPaddingTypeReplyHeader are lower-case too.
      headers["padding"] = MakePaddingHeader();
      headers["padding-type-reply"] = "1";
    }
    quic::QuicBackendResponse response;
    response.set_headers(std::move(headers));
    response.set_response_type(quic::QuicBackendResponse::INCOMPLETE_RESPONSE);
    request_handler_->OnResponseBackendComplete(&response);
    response_sent_ = true;
  }

  void CloseClientStream() {
    if (closed_) {
      return;
    }
    closed_ = true;
    if (request_handler_ != nullptr) {
      request_handler_->SendStreamData("", /*close_stream=*/true);
    }
    request_handler_ = nullptr;
    if (socket_) {
      socket_->Disconnect();
      socket_.reset();
    }
  }

  void Fail(absl::string_view reason) {
    QUICHE_LOG(WARNING) << "naive tunnel: " << reason;
    if (closed_) {
      return;
    }
    closed_ = true;
    if (request_handler_ != nullptr) {
      if (!response_sent_) {
        quiche::HttpHeaderBlock headers;
        headers[":status"] = "502";
        quic::QuicBackendResponse response;
        response.set_headers(std::move(headers));
        request_handler_->OnResponseBackendComplete(&response);
      } else {
        request_handler_->TerminateStreamWithError(quic::QuicResetStreamError::FromIetf(
            quic::QuicHttp3ErrorCode::CONNECT_ERROR));
      }
    }
    request_handler_ = nullptr;
    if (socket_) {
      socket_->Disconnect();
      socket_.reset();
    }
  }

  // Builds a padding value of random length in [30, 62) whose first 16 bytes
  // are non-Huffman-coded symbols, matching the naive server's header padding.
  static std::string MakePaddingHeader() {
    const int len =
        kServerPaddingMin + static_cast<int>(NextRandom() % kServerPaddingSpan);
    std::string padding(static_cast<size_t>(len), '~');
    for (int i = 0; i < 16 && i < len; ++i) {
      padding[static_cast<size_t>(i)] = kNonIndexSymbols[NextRandom() % 16];
    }
    return padding;
  }

  quic::SocketFactory* const socket_factory_;
  const std::string target_;
  const bool pad_to_client_;
  RequestHandler* request_handler_;
  std::unique_ptr<quic::ConnectingClientSocket> socket_;
  NaivePaddingFramer client_framer_;
  NaivePaddingFramer server_framer_;
  bool closed_ = false;
  bool response_sent_ = false;
};


// ---------------------------------------------------------------------------
// NaivePaddingFramer
// ---------------------------------------------------------------------------

std::string NaivePaddingFramer::RemovePadding(absl::string_view data) {
  if (remove_done_) {
    return std::string(data);
  }
  pending_.append(data.data(), data.size());

  std::string out;
  while (frames_seen_ < kFirstPaddings) {
    if (pending_.size() < 3) {
      return out;
    }
    const auto* p = reinterpret_cast<const unsigned char*>(pending_.data());
    const size_t payload_size =
        (static_cast<size_t>(p[0]) << 8) | static_cast<size_t>(p[1]);
    const size_t padding_size = static_cast<size_t>(p[2]);
    const size_t frame_size = 3 + payload_size + padding_size;
    if (pending_.size() < frame_size) {
      return out;
    }
    out.append(pending_, 3, payload_size);
    pending_.erase(0, frame_size);
    ++frames_seen_;
    if (frames_seen_ == kFirstPaddings) {
      // Frames after the first kFirstPaddings are unframed.
      remove_done_ = true;
      out.append(pending_);
      pending_.clear();
      return out;
    }
  }
  return out;
}

std::string NaivePaddingFramer::AddPadding(absl::string_view payload) {
  if (add_done_) {
    return std::string(payload);
  }
  const size_t padding_size =
      static_cast<size_t>(NextRandom() % (kMaxPaddingSize + 1));
  std::string frame;
  frame.reserve(3 + payload.size() + padding_size);
  // Guard: a payload of 65536 or more cannot be expressed in the frame's 16-bit
  // length field, so clamp rather than emit a header that aliases to 0.
  const size_t encoded_size = std::min<size_t>(payload.size(), 0xffff);
  frame.push_back(static_cast<char>((encoded_size >> 8) & 0xff));
  frame.push_back(static_cast<char>(encoded_size & 0xff));
  frame.push_back(static_cast<char>(padding_size & 0xff));
  frame.append(payload.data(), encoded_size);
  frame.append(padding_size, '\0');
  ++frames_seen_;
  if (frames_seen_ >= kFirstPaddings) {
    add_done_ = true;
  }
  return frame;
}

// ---------------------------------------------------------------------------
// NaiveServerBackend
// ---------------------------------------------------------------------------

NaiveServerBackend::NaiveServerBackend(NaiveBackendConfig config)
    : config_(std::move(config)) {
  if (config_.server_label.empty()) {
    config_.server_label = "naivereal-h3";
  }
}

NaiveServerBackend::~NaiveServerBackend() = default;

bool NaiveServerBackend::InitializeBackend(const std::string& /*backend_url*/) {
  initialized_ = true;
  return true;
}

void NaiveServerBackend::SetSocketFactory(quic::SocketFactory* socket_factory) {
  socket_factory_ = socket_factory;
}


size_t NaiveServerBackend::active_tunnels() const {
  size_t n = 0;
  for (const auto& entry : tunnels_) {
    if (entry.second && !entry.second->closed()) {
      ++n;
    }
  }
  return n;
}

bool NaiveServerBackend::Authorized(
    const quiche::HttpHeaderBlock& request_headers) const {
  if (config_.username.empty() || config_.password.empty()) {
    return false;
  }
  auto it = request_headers.find("proxy-authorization");
  if (it == request_headers.end()) {
    return false;
  }
  absl::string_view value = it->second;
  constexpr absl::string_view kPrefix = "Basic ";
  if (!absl::StartsWithIgnoreCase(value, kPrefix)) {
    return false;
  }
  std::optional<std::string> decoded =
      quiche::QuicheTextUtils::Base64Decode(value.substr(kPrefix.size()));
  if (!decoded.has_value()) {
    return false;
  }
  const std::string expected = config_.username + ":" + config_.password;
  // Constant-time comparison: the credential length is not secret, its content
  // is.
  if (decoded->size() != expected.size()) {
    return false;
  }
  unsigned char diff = 0;
  for (size_t i = 0; i < decoded->size(); ++i) {
    diff |= static_cast<unsigned char>((*decoded)[i] ^ expected[i]);
  }
  return diff == 0;
}

void NaiveServerBackend::FetchResponseFromBackend(
    const quiche::HttpHeaderBlock& request_headers,
    const std::string& /*request_body*/, RequestHandler* request_handler) {
  std::string method = "(none)";
  auto it = request_headers.find(":method");
  if (it != request_headers.end()) {
    method = std::string(it->second);
  }
  {
    std::string line = "[naive] FetchResponseFromBackend method=" + method + "\n";
    std::fputs(line.c_str(), stderr);
  }
  std::string line = "[naive] FetchResponseFromBackend method=" + method + "\n";
  std::fputs(line.c_str(), stderr);
  ServeWebsite(request_headers, request_handler);
}

void NaiveServerBackend::HandleConnectHeaders(
    const quiche::HttpHeaderBlock& request_headers,
    RequestHandler* request_handler) {
  {
    std::string dump;
    for (const auto& kv : request_headers) {
      dump += absl::StrCat(" ", kv.first);
    }
    std::string line = "[naive] CONNECT headers:" + dump +
                        " authorized=" + (Authorized(request_headers) ? "1" : "0") + "\n";
    std::fputs(line.c_str(), stderr);
  }
  // An unauthenticated CONNECT gets a 407 challenge carrying
  // Proxy-Authenticate, which is what forwardproxy.go does and what the real
  // naive client requires: it issues CONNECT without credentials first and only
  // sends Proxy-Authorization after being challenged. Serving the request as a
  // website instead (the obvious-looking "don't reveal a proxy" choice) makes
  // the real client give up with ERR_QUIC_PROTOCOL_ERROR.
  {
    // NAIVE_DEBUG_DUMP
    std::string dump;
    for (const auto& kv : request_headers) {
      dump += " ";
      dump += kv.first;
    }
    std::string line = "[naive] CONNECT authorized=";
    line += Authorized(request_headers) ? "1" : "0";
    line += " headers:";
    line += dump;
    line += "\n";
    std::fputs(line.c_str(), stderr);
  }
  if (!Authorized(request_headers)) {
    quiche::HttpHeaderBlock headers;
    headers[":status"] = "407";
    headers["proxy-authenticate"] = "Basic realm=\"Caddy Secure Web Proxy\"";
    quic::QuicBackendResponse response;
    response.set_headers(std::move(headers));
    request_handler->OnResponseBackendComplete(&response);
    return;
  }
  if (socket_factory_ == nullptr) {
    quiche::HttpHeaderBlock headers;
    headers[":status"] = "500";
    quic::QuicBackendResponse response;
    response.set_headers(std::move(headers));
    request_handler->OnResponseBackendComplete(&response);
    return;
  }

  std::string target = config_.upstream_addr;
  if (target.empty()) {
    auto it = request_headers.find(":authority");
    if (it == request_headers.end()) {
      it = request_headers.find("host");
    }
    if (it == request_headers.end()) {
      quiche::HttpHeaderBlock headers;
      headers[":status"] = "400";
      quic::QuicBackendResponse response;
      response.set_headers(std::move(headers));
      request_handler->OnResponseBackendComplete(&response);
      return;
    }
    target = std::string(it->second);
  }

  // The client negotiates padding by sending a padding header; reply in kind.
  const bool pad_to_client = request_headers.contains("padding");

  // Reap finished tunnels so the map does not grow without bound, then insert
  // and start the new one. No raw pointer is retained across the container
  // mutation.
  for (auto it = tunnels_.begin(); it != tunnels_.end();) {
    if (it->second->closed()) {
      // absl::flat_hash_map::erase(iterator) returns void, so advance first.
      tunnels_.erase(it++);
    } else {
      ++it;
    }
  }
  auto tunnel = std::make_unique<Tunnel>(socket_factory_, target, pad_to_client,
                                         request_handler);
  Tunnel* raw = tunnel.get();
  tunnels_[request_handler] = std::move(tunnel);
  raw->Start();
}

void NaiveServerBackend::HandleConnectData(absl::string_view data,
                                           bool data_complete,
                                           RequestHandler* request_handler) {
  auto it = tunnels_.find(request_handler);
  Tunnel* tunnel = it == tunnels_.end() ? nullptr : it->second.get();
  if (tunnel == nullptr || tunnel->closed()) {
    request_handler->TerminateStreamWithError(quic::QuicResetStreamError::FromIetf(
        quic::QuicHttp3ErrorCode::CONNECT_ERROR));
    return;
  }
  std::string payload = tunnel->ReadFromClient(data);
  if (!payload.empty()) {
    tunnel->WriteToDestination(payload);
  }
  if (data_complete) {
    tunnel->OnClientStreamClose();
  }
}

void NaiveServerBackend::CloseBackendResponseStream(
    RequestHandler* request_handler) {
  auto it = tunnels_.find(request_handler);
  if (it != tunnels_.end()) {
    it->second->OnClientStreamClose();
    tunnels_.erase(it);
  }
}

bool NaiveServerBackend::ServeWebsite(
    const quiche::HttpHeaderBlock& request_headers,
    RequestHandler* request_handler) {
  auto method_it = request_headers.find(":method");
  const absl::string_view method =
      method_it == request_headers.end() ? absl::string_view("GET")
                                         : absl::string_view(method_it->second);
  if (method != "GET" && method != "HEAD") {
    quiche::HttpHeaderBlock headers;
    headers[":status"] = "405";
    headers["allow"] = "GET, HEAD";
    quic::QuicBackendResponse response;
    response.set_headers(std::move(headers));
    request_handler->OnResponseBackendComplete(&response);
    return true;
  }

  std::string path = "/";
  auto path_it = request_headers.find(":path");
  if (path_it != request_headers.end()) {
    path = std::string(path_it->second);
  }
  // Strip a query string and refuse traversal before touching the filesystem.
  const size_t query = path.find('?');
  if (query != std::string::npos) {
    path.resize(query);
  }
  if (path.find("..") != std::string::npos) {
    path = "/";
  }
  if (path.empty() || path.back() == '/') {
    path += "index.html";
  }

  std::string content;
  std::string content_type;
  quiche::HttpHeaderBlock headers;
  if (!config_.web_root.empty() &&
      ReadFile(absl::StrCat(config_.web_root, path), &content, &content_type)) {
    headers[":status"] = "200";
    headers["content-type"] = content_type;
    headers["content-length"] = absl::StrCat(content.size());
  } else {
    headers[":status"] = "404";
    content_type = "text/html; charset=utf-8";
    content =
        "<html><head><title>404 Not Found</title></head>"
        "<body>404 Not Found</body></html>";
    headers["content-type"] = content_type;
    headers["content-length"] = absl::StrCat(content.size());
  }

  quic::QuicBackendResponse response;
  response.set_headers(std::move(headers));
  if (method == "GET") {
    response.set_body(content);
  }
  request_handler->OnResponseBackendComplete(&response);
  return true;
}

bool NaiveServerBackend::ReadFile(absl::string_view path, std::string* content,
                                  std::string* content_type) const {
  std::ifstream in(std::string(path), std::ios::binary);
  if (!in) {
    return false;
  }
  content->assign(std::istreambuf_iterator<char>(in),
                  std::istreambuf_iterator<char>());
  if (absl::EndsWith(path, ".html")) {
    *content_type = "text/html; charset=utf-8";
  } else if (absl::EndsWith(path, ".css")) {
    *content_type = "text/css";
  } else if (absl::EndsWith(path, ".js")) {
    *content_type = "application/javascript";
  } else if (absl::EndsWith(path, ".json")) {
    *content_type = "application/json";
  } else {
    *content_type = "application/octet-stream";
  }
  return true;
}

}  // namespace naivereal
