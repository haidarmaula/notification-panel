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

FROM node:22.23.4-alpine@sha256:0e1754758c2df4cfa3e68d59e2e1b7374bb7fd9ea6328eaaacf15cc934af8202 AS dashboard-build
WORKDIR /app
COPY web/dashboard/package.json web/dashboard/pnpm-lock.yaml ./
RUN npm install --global pnpm@11.25.0 --ignore-scripts && pnpm install --frozen-lockfile
COPY web/dashboard ./
RUN pnpm build && pnpm budget

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

FROM runtime AS dashboard
COPY --from=build /out/dashboard /dashboard
COPY --from=dashboard-build /app/dist /app
ENTRYPOINT ["/dashboard", "--deployment"]
