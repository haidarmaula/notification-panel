# Build a single process image using its command name as the target.
FROM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

RUN go build -mod=readonly -trimpath -buildvcs=false -o /out/ ./cmd/...

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
STOPSIGNAL SIGTERM

FROM runtime AS worker
COPY --from=build /out/worker /worker
ENTRYPOINT ["/worker"]

FROM runtime AS scheduler
COPY --from=build /out/scheduler /scheduler
ENTRYPOINT ["/scheduler"]

FROM runtime AS bootstrap
COPY --from=build /out/bootstrap /bootstrap
ENTRYPOINT ["/bootstrap", "--deployment"]

FROM runtime AS requeue
COPY --from=build /out/requeue /requeue
ENTRYPOINT ["/requeue"]

FROM runtime AS retention
COPY --from=build /out/retention /retention
ENTRYPOINT ["/retention"]

FROM runtime AS server
COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server", "--deployment"]
