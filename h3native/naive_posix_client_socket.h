// Copyright 2026. Licensed under the BSD-3-Clause license.
//
// A minimal ConnectingClientSocket backed by a plain POSIX socket, for the naive
// H3 server's CONNECT tunnels.
//
// QUICHE's EventLoopConnectingClientSocket defers its async work to the server's
// event loop, which a QuicSimpleServerBackend cannot reach. Its
// ConnectBlocking also does not complete for a socket obtained from
// EventLoopSocketFactory, so a tunnel built on it stalls before it can send the
// CONNECT response.
//
// A tunnel needs none of that machinery: it connects once and then does
// blocking reads and writes on its own thread, which is what a proxy tunnel
// wants anyway. This class is deliberately small and owns exactly one fd.

#ifndef NAIVEREAL_NAIVE_POSIX_CLIENT_SOCKET_H_
#define NAIVEREAL_NAIVE_POSIX_CLIENT_SOCKET_H_

#include <atomic>
#include <string>
#include <thread>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "quiche/quic/core/connecting_client_socket.h"
#include "quiche/quic/platform/api/quic_socket_address.h"
#include "quiche/common/quiche_mem_slice.h"

namespace naivereal {

class NaivePosixClientSocket : public quic::ConnectingClientSocket {
 public:
  explicit NaivePosixClientSocket(quic::QuicSocketAddress peer_address);
  ~NaivePosixClientSocket() override;

  NaivePosixClientSocket(const NaivePosixClientSocket&) = delete;
  NaivePosixClientSocket& operator=(const NaivePosixClientSocket&) = delete;

  // quic::ConnectingClientSocket:
  absl::Status ConnectBlocking() override;
  void ConnectAsync() override;
  void Disconnect() override;
  absl::StatusOr<quic::QuicSocketAddress> GetLocalAddress() override;
  absl::StatusOr<quiche::QuicheMemSlice> ReceiveBlocking(
      quic::QuicByteCount max_size) override;
  void ReceiveAsync(quic::QuicByteCount max_size) override;
  absl::Status SendBlocking(std::string data) override;
  absl::Status SendBlocking(quiche::QuicheMemSlice data) override;
  void SendAsync(std::string data) override;
  void SendAsync(quiche::QuicheMemSlice data) override;

  void set_async_visitor(AsyncVisitor* visitor) { async_visitor_ = visitor; }

 private:
  // Runs the blocking read loop until Disconnect() is called.
  void ReadLoop(quic::QuicByteCount max_size);

  const quic::QuicSocketAddress peer_address_;
  int fd_ = -1;
  std::atomic<bool> stopped_{false};
  std::thread read_thread_;
  AsyncVisitor* async_visitor_ = nullptr;
};

}  // namespace naivereal

#endif  // NAIVEREAL_NAIVE_POSIX_CLIENT_SOCKET_H_
