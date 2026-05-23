FROM node:25.9.0 AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npx vite build

FROM golang:1.26.2 AS backend
WORKDIR /app
ENV GOPROXY=https://goproxy.cn,direct
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist cmd/server/web/dist
RUN CGO_ENABLED=0 go build -o yaml-sync ./cmd/server/...

FROM rockylinux:9-minimal
COPY --from=backend /app/yaml-sync /usr/local/bin/yaml-sync
ENV TZ=Asia/Shanghai
EXPOSE 8080
VOLUME /data
CMD ["yaml-sync"]
