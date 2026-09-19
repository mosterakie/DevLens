# 多阶段构建：编译期用完整工具链，运行期只带二进制。
#
# 构建镜像的 Go 版本必须不低于 go.mod 里声明的版本，
# 否则 go build 会直接拒绝。go.mod 声明 1.27.1。
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 静态编译，便于跑在 distroless 上。
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/worker ./cmd/worker

# 用 :nonroot 变体。static-debian12 的默认 tag 是 root（User=0），
# 只有 :nonroot 才以 uid 65532 运行。这一点容易记反。
#
# distroless 不带 shell，所以容器内无法执行命令排查问题，
# 换来的好处是攻击面比 alpine 小很多。
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/web /app/web

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/api"]