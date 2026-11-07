FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nodexlm ./cmd/nodexlm

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 nodexlm && useradd --uid 10001 --gid 10001 --no-create-home nodexlm \
    && mkdir /data && chown nodexlm:nodexlm /data && chmod 700 /data
COPY --from=build /out/nodexlm /usr/local/bin/nodexlm
USER 10001:10001
ENV NODEXLM_HOME=/data
VOLUME /data
ENTRYPOINT ["nodexlm"]
CMD ["host"]
