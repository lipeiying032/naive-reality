// Copyright 2026. Licensed under the BSD-3-Clause license.

#include "naive_posix_client_socket.h"

#include <cerrno>
#include <cstring>
#include <string>
#include <utility>

#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>

#include "absl/status/status.h"
#include "absl/strings/str_cat.h"
#include "quiche/common/platform/api/quiche_logging.h"
#include "common/quiche_mem_slice.h"
#include "common/simple_buffer_allocator.h"
#include "quiche/common/quiche_callbacks.h"

namespace naivereal {

namespace {

absl::Status ErrnoStatus(absl::string_view what) {
  return absl::ErrnoToStatus(errno, std::string(what));
}

// 8 MiB matches the buffer sizes the Hysteria2-aligned deployment expects, so a
// single tunnel stream is not the constraint on throughput.
constexpr int kSocketBufferSize = 8 * 1024 * 1024;

}  // namespace

NaivePosixClientSocket::NaivePosixClientSocket(quic::QuicSocketAddress peer_address)
    : peer_address_(peer_address) {}

NaivePosixClientSocket::~NaivePosixClientSocket() {
  Disconnect();
  if (read_thread_.joinable()) {
    read_thread_.join();
  }
}

absl::Status NaivePosixClientSocket::ConnectBlocking() {
  if (fd_ >= 0) {
    return absl::FailedPreconditionError("socket already connected");
  }
  fd_ = ::socket(peer_address_.host().IsIPv4() ? AF_INET : AF_INET6, SOCK_STREAM, 0);
  if (fd_ < 0) {
    return ErrnoStatus("socket");
  }
  int one = 1;
  ::setsockopt(fd_, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));
  ::setsockopt(fd_, SOL_SOCKET, SO_RCVBUF, &kSocketBufferSize,
               sizeof(kSocketBufferSize));
  ::setsockopt(fd_, SOL_SOCKET, SO_SNDBUF, &kSocketBufferSize,
               sizeof(kSocketBufferSize));

  // QuicSocketAddress already knows how to hand back a sockaddr_storage.
  sockaddr_storage storage = peer_address_.generic_address();
  const socklen_t len = sizeof(storage);

  if (::connect(fd_, reinterpret_cast<sockaddr*>(&storage), len) != 0) {
    absl::Status status = ErrnoStatus("connect");
    ::close(fd_);
    fd_ = -1;
    return status;
  }
  return absl::OkStatus();
}

void NaivePosixClientSocket::ConnectAsync() {
  // The tunnel only ever connects from its own thread, so the async entry point
  // simply performs the blocking connect and reports the result.
  absl::Status status = ConnectBlocking();
  if (async_visitor_ != nullptr) {
    async_visitor_->ConnectComplete(status);
  }
}

void NaivePosixClientSocket::Disconnect() {
  stopped_.store(true);
  if (fd_ >= 0) {
    // shutdown() unblocks a recv() in the read thread before close().
    ::shutdown(fd_, SHUT_RDWR);
    ::close(fd_);
    fd_ = -1;
  }
}

absl::StatusOr<quic::QuicSocketAddress> NaivePosixClientSocket::GetLocalAddress() {
  return absl::UnimplementedError("GetLocalAddress is not needed by the tunnel");
}

absl::StatusOr<quiche::QuicheMemSlice> NaivePosixClientSocket::ReceiveBlocking(
    quic::QuicByteCount max_size) {
  if (fd_ < 0) {
    return absl::FailedPreconditionError("socket is not connected");
  }
  std::string buffer(max_size, '\0');
  const ssize_t n = ::recv(fd_, buffer.data(), buffer.size(), 0);
  if (n < 0) {
    return ErrnoStatus("recv");
  }
  if (n == 0) {
    return absl::UnavailableError("peer closed the connection");
  }
  buffer.resize(static_cast<size_t>(n));
  // QuicheMemSlice is move-only with several converting constructors, which
  // makes  ambiguous for absl::StatusOr: absl can pick the
  // Status constructor and abort with "An OK status is not a valid constructor
  // argument to StatusOr<T>". Construct it into a StatusOr explicitly.
  quiche::QuicheBuffer owned = quiche::QuicheBuffer::Copy(
      quiche::SimpleBufferAllocator::Get(), absl::string_view(buffer));
  // Build the slice as a named value and move it in. Returning the slice
  // directly, or using absl::in_place (which selects a single-argument
  // constructor), makes absl pick StatusOr's Status constructor and abort with
  // "An OK status is not a valid constructor argument to StatusOr<T>".
  quiche::QuicheMemSlice slice(std::move(owned));
  absl::StatusOr<quiche::QuicheMemSlice> result(std::move(slice));
  return result;
}

void NaivePosixClientSocket::ReceiveAsync(quic::QuicByteCount max_size) {
  // Dedicated reader thread: a tunnel wants blocking reads off the event loop.
  if (read_thread_.joinable()) {
    return;
  }
  read_thread_ = std::thread([this, max_size]() { ReadLoop(max_size); });
}

void NaivePosixClientSocket::ReadLoop(quic::QuicByteCount max_size) {
  while (!stopped_.load()) {
    absl::StatusOr<quiche::QuicheMemSlice> data = ReceiveBlocking(max_size);
    if (async_visitor_ == nullptr) {
      return;
    }
    async_visitor_->ReceiveComplete(std::move(data));
    if (stopped_.load()) {
      return;
    }
  }
}

absl::Status NaivePosixClientSocket::SendBlocking(std::string data) {
  if (fd_ < 0) {
    return absl::FailedPreconditionError("socket is not connected");
  }
  size_t written = 0;
  while (written < data.size()) {
    const ssize_t n = ::send(fd_, data.data() + written, data.size() - written,
                             MSG_NOSIGNAL);
    if (n < 0) {
      if (errno == EINTR) {
        continue;
      }
      return ErrnoStatus("send");
    }
    written += static_cast<size_t>(n);
  }
  return absl::OkStatus();
}

absl::Status NaivePosixClientSocket::SendBlocking(quiche::QuicheMemSlice data) {
  return SendBlocking(std::string(data.AsStringView()));
}

void NaivePosixClientSocket::SendAsync(std::string data) {
  absl::Status status = SendBlocking(std::move(data));
  if (async_visitor_ != nullptr) {
    async_visitor_->SendComplete(status);
  }
}

void NaivePosixClientSocket::SendAsync(quiche::QuicheMemSlice data) {
  SendAsync(std::string(data.AsStringView()));
}

}  // namespace naivereal
