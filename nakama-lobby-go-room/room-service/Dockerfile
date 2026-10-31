# Build context: nakama-lobby-go-room/ (the room service needs ../proto).
FROM heroiclabs/nakama-pluginbuilder:3.41.0 AS build
WORKDIR /src
COPY proto/ proto/
COPY room-service/ room-service/
WORKDIR /src/room-service
RUN CGO_ENABLED=0 go build -trimpath -o /room-service ./cmd/room-service

FROM scratch
COPY --from=build /room-service /room-service
EXPOSE 8923 8924
ENTRYPOINT ["/room-service"]
