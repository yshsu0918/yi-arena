// Command lobby-plugin is the Nakama Go plugin for the lobby service. Nakama
// provides accounts, friends, direct messages, notifications and status; this
// plugin adds invites, the room list and entry tickets, and talks to the room
// service over gRPC.
//
// Build it with heroiclabs/nakama-pluginbuilder:3.41.0 (see scripts/build-plugin.sh).
package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

type config struct {
	ticketSecret      []byte
	roomAddr          string
	inviteTimeout     time.Duration
	ticketTTL         time.Duration
	reconcileInterval time.Duration
}

func loadConfig(env map[string]string) (config, error) {
	c := config{
		ticketSecret:      []byte(env["TICKET_SECRET"]),
		roomAddr:          env["ROOM_GRPC_ADDR"],
		inviteTimeout:     seconds(env["INVITE_TIMEOUT_SEC"], 60),
		ticketTTL:         seconds(env["TICKET_TTL_SEC"], 60),
		reconcileInterval: seconds(env["RECONCILE_INTERVAL_SEC"], 3),
	}
	if len(c.ticketSecret) == 0 {
		return c, errors.New("runtime env TICKET_SECRET is required")
	}
	if c.roomAddr == "" {
		return c, errors.New("runtime env ROOM_GRPC_ADDR is required")
	}
	return c, nil
}

func seconds(s string, def int) time.Duration {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(def) * time.Second
}

//goland:noinspection GoUnusedExportedFunction
func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	env, _ := ctx.Value(runtime.RUNTIME_CTX_ENV).(map[string]string)
	cfg, err := loadConfig(env)
	if err != nil {
		return err
	}
	// grpc.NewClient does not dial: the room service may start after Nakama.
	conn, err := grpc.NewClient(cfg.roomAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	l := newLobby(cfg, logger, nk, roompb.NewRoomServiceClient(conn))

	rpcs := map[string]func(context.Context, runtime.Logger, *sql.DB, runtime.NakamaModule, string) (string, error){
		"invite":         l.rpcInvite,
		"invite_reply":   l.rpcInviteReply,
		"list_rooms":     l.rpcListRooms,
		"reissue_ticket": l.rpcReissueTicket,
		"send_note":      l.rpcSendNote,
		"room_event":     l.rpcRoomEvent,  // server to server (HTTP key)
		"lobby_stats":    l.rpcLobbyStats, // server to server (HTTP key)
	}
	for id, fn := range rpcs {
		if err := initializer.RegisterRpc(id, fn); err != nil {
			return err
		}
	}
	if err := initializer.RegisterEventSessionStart(l.onSessionStart); err != nil {
		return err
	}
	go l.reconcileLoop()
	logger.Info("lobby plugin loaded: room service %s, invite timeout %s", cfg.roomAddr, cfg.inviteTimeout)
	return nil
}
