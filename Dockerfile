# 多阶段构建：编译期用完整工具链，运行期只带二进制。
FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 静态编译，便于跑在 distroless 上。
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/web /app/web

# distroless 默认非 root，不需要额外 USER 指令。
# 没有 shell，攻击面比 alpine 小。

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/api"]