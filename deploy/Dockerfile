# naivereal server image: official naive server + REALITY frontend.
# Build:  docker build -f deploy/Dockerfile -t naivereal .
# Run:    docker run -d --network host -v $PWD/frontend.toml:/etc/naivereal/frontend.toml:ro -v $PWD/naive.json:/etc/naivereal/naive.json:ro naivereal
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tini \
    && rm -rf /var/lib/apt/lists/*

COPY naive /usr/local/bin/naive
COPY naivereal-frontend /usr/local/bin/naivereal-frontend
COPY frontend/frontend.toml.example /etc/naivereal/frontend.toml
COPY deploy/naive.json.example /etc/naivereal/naive.json

# Official naive server: h1 CONNECT proxy on 127.0.0.1:8080 (set your own credentials!)
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/bin/sh", "-c", "/usr/local/bin/naive /etc/naivereal/naive.json & exec /usr/local/bin/naivereal-frontend /etc/naivereal/frontend.toml"]
