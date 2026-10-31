// Package rpcserver is the room service's gRPC API, called by the lobby only.
package rpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/room"
)

type Server struct {
	roompb.UnimplementedRoomServiceServer
	Rooms        *room.Manager
	PublicWSBase string // e.g. ws://localhost:8923
	InstanceID   string
}

func (s *Server) CreateRoom(_ context.Context, req *roompb.CreateRoomRequest) (*roompb.CreateRoomResponse, error) {
	id := req.GetRoomId()
	if id == "" || len(id) > 64 || len(req.GetMembers()) == 0 || len(req.GetMembers()) > 8 {
		return nil, status.Error(codes.InvalidArgument, "room_id and 1-8 members required")
	}
	seen := map[string]bool{}
	for _, m := range req.GetMembers() {
		if m.GetUserId() == "" || seen[m.GetUserId()] {
			return nil, status.Error(codes.InvalidArgument, "member user_id missing or repeated")
		}
		seen[m.GetUserId()] = true
	}
	if _, err := s.Rooms.Create(id, req.GetMembers()); err != nil {
		if errors.Is(err, room.ErrExists) {
			return nil, status.Error(codes.AlreadyExists, "room exists")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &roompb.CreateRoomResponse{RoomId: id, WsUrl: s.PublicWSBase + "/rooms/" + id}, nil
}

func (s *Server) ListRooms(context.Context, *roompb.ListRoomsRequest) (*roompb.ListRoomsResponse, error) {
	return &roompb.ListRoomsResponse{Rooms: s.Rooms.List(), InstanceId: s.InstanceID}, nil
}

func (s *Server) CloseRoom(_ context.Context, req *roompb.CloseRoomRequest) (*roompb.CloseRoomResponse, error) {
	return &roompb.CloseRoomResponse{Closed: s.Rooms.Close(req.GetRoomId(), "admin")}, nil
}
