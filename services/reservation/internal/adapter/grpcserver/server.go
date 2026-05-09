// Package grpcserver — primary adapter, gRPC handler untuk ReservationService.
//
// Implementasi reservationv1.ReservationServiceServer (proto-generated). Server
// ini di-register ke *grpc.Server di main.go melalui RegisterReservationServiceServer.
//
// Idempotency:
//   - Header "x-idempotency-key" (gRPC metadata) digunakan untuk dedup mutating RPCs.
//   - Response di-cache di pkg/idempotency.Store. Replay mengembalikan exactly same body.
package grpcserver

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/ajiperdana/parkir-pintar/pkg/errs"
	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	reservationv1 "github.com/ajiperdana/parkir-pintar/proto/gen/reservation/v1"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

// BookingFeeIDR — flat fee per reservation (use case requirement).
const BookingFeeIDR = int64(5_000)

// Server — gRPC server untuk ReservationService.
//
// Implements reservationv1.ReservationServiceServer (compiled from
// proto/reservation/v1/reservation.proto via `make proto`).
//
// NOTE: field names sengaja berbeda dengan method names (CheckIn vs CheckInUC)
// karena Go tidak mengizinkan field & method dengan nama sama di struct yang
// sama, dan proto-generated interface mengharuskan method bernama CheckIn dll.
type Server struct {
	reservationv1.UnimplementedReservationServiceServer

	CreateRes      *usecase.CreateReservation
	CheckInUC      *usecase.CheckIn
	CheckOutUC     *usecase.CheckOut
	CancelUC       *usecase.Cancel
	AvailabilityUC *usecase.GetAvailability
	Reservations   usecase.ReservationRepo
	Spots          usecase.SpotRepo // untuk ListSpots query
	Idempotency    idempotency.Store
	IdemTTL        time.Duration
	Logger         *zap.Logger
}

// Register — daftarkan ke gRPC server.
func (s *Server) Register(grpcServer *grpc.Server) {
	reservationv1.RegisterReservationServiceServer(grpcServer, s)
}

// CreateReservation — booking.
//
// Idempotency: kalau header x-idempotency-key ada, response di-cache (24h TTL).
// Replay request dengan key yang sama mengembalikan response identik.
func (s *Server) CreateReservation(ctx context.Context, req *reservationv1.CreateReservationRequest) (*reservationv1.CreateReservationResponse, error) {
	idemKey := grpcutil.IdempotencyKeyFromContext(ctx)

	// Cache key memakai marshalled request (deterministic).
	payload, _ := json.Marshal(req)

	res, err := idempotency.Wrap(ctx, s.Idempotency, idemKey, "reservation:create", payload, s.IdemTTL,
		func(ctx context.Context) (int, []byte, error) {
			out, err := s.doCreate(ctx, req)
			if err != nil {
				return 0, nil, err
			}
			body, _ := json.Marshal(map[string]any{
				"reservation_id": out.Reservation.ID.String(),
				"driver_id":      out.Reservation.DriverID,
				"spot_id":        out.Spot.ID.String(),
				"spot_code":      out.Spot.Code,
				"floor_level":    out.Spot.FloorLevel,
				"plate_no":       out.Reservation.PlateNo,
				"vehicle_type":   string(out.Reservation.VehicleType),
				"state":          string(out.Reservation.State),
				"payment_mode":   string(out.Reservation.PaymentMode),
				"start_at":       out.Reservation.StartAt,
				"expires_at":     out.Reservation.ExpiresAt,
				"created_at":     out.Reservation.CreatedAt,
			})
			return 201, body, nil
		},
	)
	if err != nil {
		return nil, errs.ToStatus(err)
	}

	// Decode cache → reconstruct proto response.
	var c struct {
		ReservationID string    `json:"reservation_id"`
		DriverID      string    `json:"driver_id"`
		SpotID        string    `json:"spot_id"`
		SpotCode      string    `json:"spot_code"`
		FloorLevel    int       `json:"floor_level"`
		PlateNo       string    `json:"plate_no"`
		VehicleType   string    `json:"vehicle_type"`
		State         string    `json:"state"`
		PaymentMode   string    `json:"payment_mode"`
		StartAt       time.Time `json:"start_at"`
		ExpiresAt     time.Time `json:"expires_at"`
		CreatedAt     time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(res.Body, &c); err != nil {
		return nil, errs.ToStatus(err)
	}

	return &reservationv1.CreateReservationResponse{
		Reservation: &reservationv1.Reservation{
			Id:       c.ReservationID,
			DriverId: c.DriverID,
			Spot: &commonv1.Spot{
				Id:          c.SpotID,
				Code:        c.SpotCode,
				FloorLevel:  int32(c.FloorLevel),
				VehicleType: vehicleTypeToProto(domain.VehicleType(c.VehicleType)),
				Status:      commonv1.SpotStatus_HELD,
			},
			PlateNo:        c.PlateNo,
			State:          stateToProto(domain.State(c.State)),
			StartAt:        timeToTs(c.StartAt),
			ExpiresAt:      timeToTs(c.ExpiresAt),
			AssignmentMode: assignmentModeToProto(assignmentModeFromProto(req.GetMode())),
			PaymentMode:    paymentModeToProto(domain.PaymentMode(c.PaymentMode)),
			CreatedAt:      timeToTs(c.CreatedAt),
		},
		BookingFee: &commonv1.Money{Amount: BookingFeeIDR, Currency: "IDR"},
	}, nil
}

func (s *Server) doCreate(ctx context.Context, req *reservationv1.CreateReservationRequest) (*usecase.CreateReservationOutput, error) {
	// start_at kosong → now (booking instant — driver mau parkir sekarang).
	// Usecase juga handle default ini sebagai defense-in-depth.
	startAt := tsToTime(req.GetStartAt())
	if startAt.IsZero() {
		startAt = time.Now().UTC()
	}

	vt, err := vehicleTypeFromProto(req.GetVehicleType())
	if err != nil {
		return nil, err
	}
	payMode, err := paymentModeFromProto(req.GetPaymentMode())
	if err != nil {
		return nil, err
	}

	in := usecase.CreateReservationInput{
		DriverID:       req.GetDriverId(),
		PlateNo:        req.GetPlateNo(),
		VehicleType:    vt,
		Mode:           assignmentModeFromProto(req.GetMode()),
		PaymentMode:    payMode,
		StartAt:        startAt,
		IdempotencyKey: grpcutil.IdempotencyKeyFromContext(ctx),
	}
	if sid := req.GetSpotId(); sid != "" {
		id, err := parseUUID(sid)
		if err != nil {
			return nil, domain.ErrSpotNotFound
		}
		in.SpotID = &id
	}
	return s.CreateRes.Execute(ctx, in)
}

// GetReservation — query by ID.
func (s *Server) GetReservation(ctx context.Context, req *reservationv1.GetReservationRequest) (*reservationv1.Reservation, error) {
	id, err := parseUUID(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrReservationNotFound)
	}
	r, err := s.Reservations.GetByID(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return reservationToProto(r), nil
}

// CancelReservation — transition CONFIRMED → CANCELLED.
func (s *Server) CancelReservation(ctx context.Context, req *reservationv1.CancelReservationRequest) (*reservationv1.Reservation, error) {
	id, err := parseUUID(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrReservationNotFound)
	}
	r, err := s.CancelUC.Execute(ctx, id, req.GetReason())
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return reservationToProto(r), nil
}

// CheckIn — driver tiba di parkir.
func (s *Server) CheckIn(ctx context.Context, req *reservationv1.CheckInRequest) (*reservationv1.Reservation, error) {
	id, err := parseUUID(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrReservationNotFound)
	}
	r, err := s.CheckInUC.Execute(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return reservationToProto(r), nil
}

// CheckOut — driver pulang.
func (s *Server) CheckOut(ctx context.Context, req *reservationv1.CheckOutRequest) (*reservationv1.Reservation, error) {
	id, err := parseUUID(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrReservationNotFound)
	}
	r, err := s.CheckOutUC.Execute(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return reservationToProto(r), nil
}

// GetAvailability — realtime count per floor.
//
// req.parking_area_id di-ignore (single-area). Field tetap di proto untuk
// forward-compat ekspansi multi-area.
func (s *Server) GetAvailability(ctx context.Context, _ *reservationv1.GetAvailabilityRequest) (*reservationv1.GetAvailabilityResponse, error) {
	avail, err := s.AvailabilityUC.Execute(ctx)
	if err != nil {
		return nil, errs.ToStatus(err)
	}

	floors := make([]*reservationv1.FloorAvailability, 0, len(avail.Floors))
	totalCar, totalMotor := int32(0), int32(0)
	for _, f := range avail.Floors {
		floors = append(floors, &reservationv1.FloorAvailability{
			Level:          int32(f.Level),
			CarAvailable:   int32(f.CarAvailable),
			CarCapacity:    int32(f.CarCapacity),
			MotorAvailable: int32(f.MotorAvailable),
			MotorCapacity:  int32(f.MotorCapacity),
		})
		totalCar += int32(f.CarAvailable)
		totalMotor += int32(f.MotorAvailable)
	}
	return &reservationv1.GetAvailabilityResponse{
		ParkingAreaId:       avail.ParkingAreaID.String(),
		ParkingAreaName:     avail.ParkingAreaName,
		TotalCarAvailable:   totalCar,
		TotalMotorAvailable: totalMotor,
		Floors:              floors,
		AsOf:                timeToTs(avail.AsOf),
	}, nil
}

// ListSpots — list spot grouped per floor, untuk USER-assigned booking flow.
//
// Query params (via grpc-gateway):
//
//	GET /v1/spots?vehicle_type=CAR&status=AVAILABLE
//	GET /v1/spots?floor_level=2&vehicle_type=CAR
//
// Response: floors[].spots[] — match layout fisik gedung. Driver UI render
// per-floor selector tanpa perlu reconstruct grouping.
//
// Default: return semua spot match filter, group per floor. No pagination —
// dataset kecil (max 400 spot di ParkirPintar). Lihat docs/architecture/adr/
// 0011 untuk catatan ekspansi mall besar (re-introduce pagination).
func (s *Server) ListSpots(ctx context.Context, req *reservationv1.ListSpotsRequest) (*reservationv1.ListSpotsResponse, error) {
	filter := usecase.SpotFilter{
		Limit: 1000, // single-area assumption — fetch all match filter
	}

	if req.GetVehicleType() != commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED {
		vt, err := vehicleTypeFromProto(req.GetVehicleType())
		if err != nil {
			return nil, errs.ToStatus(err)
		}
		filter.VehicleType = &vt
	}
	if req.GetStatus() != commonv1.SpotStatus_SPOT_STATUS_UNSPECIFIED {
		st := spotStatusFromProto(req.GetStatus())
		filter.Status = &st
	}
	if req.FloorLevel != nil {
		fl := int(req.GetFloorLevel())
		filter.FloorLevel = &fl
	}

	spots, total, err := s.Spots.List(ctx, filter)
	if err != nil {
		return nil, errs.ToStatus(err)
	}

	// Group by floor level. Spots dari repo sudah ORDER BY level, code.
	type floorBucket struct {
		spots     []*commonv1.Spot
		available int32
	}
	groups := make(map[int]*floorBucket)
	floorOrder := make([]int, 0)

	for _, sp := range spots {
		bucket, ok := groups[sp.FloorLevel]
		if !ok {
			bucket = &floorBucket{}
			groups[sp.FloorLevel] = bucket
			floorOrder = append(floorOrder, sp.FloorLevel)
		}
		bucket.spots = append(bucket.spots, spotToProto(sp))
		if sp.Status == domain.SpotAvailable {
			bucket.available++
		}
	}

	floors := make([]*reservationv1.FloorSpots, 0, len(floorOrder))
	for _, lvl := range floorOrder {
		b := groups[lvl]
		floors = append(floors, &reservationv1.FloorSpots{
			Level:     int32(lvl),
			Spots:     b.spots,
			Capacity:  int32(len(b.spots)), // count match filter (status+vehicle_type)
			Available: b.available,
		})
	}

	return &reservationv1.ListSpotsResponse{
		Floors: floors,
		Total:  int32(total),
	}, nil
}

// reservationToProto — domain → proto.
// reservationToProto — domain → proto.
func reservationToProto(r *domain.Reservation) *reservationv1.Reservation {
	if r == nil {
		return nil
	}
	return &reservationv1.Reservation{
		Id:       r.ID.String(),
		DriverId: r.DriverID,
		Spot: &commonv1.Spot{
			Id:          r.SpotID.String(),
			VehicleType: vehicleTypeToProto(r.VehicleType),
		},
		PlateNo:        r.PlateNo,
		State:          stateToProto(r.State),
		StartAt:        timeToTs(r.StartAt),
		ExpiresAt:      timeToTs(r.ExpiresAt),
		CheckinAt:      timePtrToTs(r.CheckInAt),
		CheckoutAt:     timePtrToTs(r.CheckOutAt),
		AssignmentMode: assignmentModeToProto(r.AssignmentMode),
		PaymentMode:    paymentModeToProto(r.PaymentMode),
		CreatedAt:      timeToTs(r.CreatedAt),
	}
}
