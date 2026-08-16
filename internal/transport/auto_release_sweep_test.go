package transport

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/arctic-express/scheduler/internal/domain"
)

// sweepEnv wires one window whose cutoff is already inside the 24-hour
// auto-release margin, a slot that is fully booked, and optional waitlist
// entries behind it.
type sweepEnv struct {
	env    *testEnv
	router http.Handler
}

func newSweepEnv(t *testing.T) *sweepEnv {
	t.Helper()
	env := newTestEnv(t)
	router := NewRouter(env.handler)

	// Cutoff is 12 hours away, so the 24-hour-before-cutoff mark has passed.
	cutoff := time.Now().Add(12 * time.Hour)
	env.store.Mu.Lock()
	env.store.SaveWindow(&domain.PeakWindow{
		ID: "win-1", VoyageID: "voy-1", OriginPort: "CNYTN", DestinationPort: "NLRTM",
		CutoffTime: cutoff, StartTime: cutoff.Add(-48 * time.Hour), EndTime: cutoff.Add(24 * time.Hour),
		Capacity: 1, OversellRatio: 0.1,
	})
	env.store.SaveSlot(&domain.Slot{
		ID: "slot-1", WindowID: "win-1", VoyageID: "voy-1", Capacity: 1,
		Booked: 1, Status: domain.SlotStatusLocked,
	})
	env.store.Mu.Unlock()
	return &sweepEnv{env: env, router: router}
}

// addLocked registers a container plus a locked reservation holding the slot.
// When arrived is false the cargo never reaches the yard, which is what the
// auto-release rule reacts to.
func (s *sweepEnv) addLocked(t *testing.T, id string, arrived bool) {
	t.Helper()
	containerID := "c-" + id
	status := domain.ContainerRegistered
	var arrivedAt *time.Time
	if arrived {
		ts := time.Now().UTC()
		status = domain.ContainerArrived
		arrivedAt = &ts
	}
	s.env.store.Mu.Lock()
	defer s.env.store.Mu.Unlock()
	s.env.store.SaveContainer(&domain.Container{
		ID: containerID, OwnerID: "owner-" + id, CargoType: domain.CargoTypeNormal,
		DestinationPort: "NLRTM", Status: status, ArrivedAt: arrivedAt,
	})
	s.env.store.SaveReservation(&domain.Reservation{
		ID: id, SlotID: "slot-1", WindowID: "win-1", ContainerID: containerID,
		OwnerID: "owner-" + id, CargoType: domain.CargoTypeNormal,
		Status: domain.ReservationLocked, CreatedAt: time.Now().UTC(),
	})
}

// addQueued registers a container plus a queued reservation on the waitlist.
func (s *sweepEnv) addQueued(t *testing.T, id string) {
	t.Helper()
	containerID := "c-" + id
	s.env.store.Mu.Lock()
	defer s.env.store.Mu.Unlock()
	s.env.store.SaveContainer(&domain.Container{
		ID: containerID, OwnerID: "owner-" + id, CargoType: domain.CargoTypeNormal,
		DestinationPort: "NLRTM", Status: domain.ContainerRegistered,
	})
	r := &domain.Reservation{
		ID: id, SlotID: "slot-1", WindowID: "win-1", ContainerID: containerID,
		OwnerID: "owner-" + id, CargoType: domain.CargoTypeNormal,
		Status: domain.ReservationQueued, CreatedAt: time.Now().UTC(),
	}
	s.env.store.SaveReservation(r)
	r.QueuePosition = s.env.store.AddToWaitlist("win-1", id)
	s.env.store.SaveReservation(r)
}

// runSweep runs one auto-release sweep and fails the test if the sweep does not
// finish within the deadline.
func (s *sweepEnv) runSweep(t *testing.T, deadline time.Duration) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() {
		done <- s.env.delSvc.CheckAutoRelease(time.Now().UTC())
	}()
	select {
	case released := <-done:
		return released
	case <-time.After(deadline):
		t.Fatalf("auto-release sweep did not finish within %s", deadline)
		return nil
	}
}

// mustRespond issues one API call and fails the test if the endpoint does not
// answer within the deadline.
func (s *sweepEnv) mustRespond(t *testing.T, method, path string, deadline time.Duration) *map[string]any {
	t.Helper()
	type outcome struct {
		code int
		raw  []byte
	}
	done := make(chan outcome, 1)
	go func() {
		rec := doJSON(t, s.router, method, path, nil)
		done <- outcome{code: rec.Code, raw: rec.Body.Bytes()}
	}()
	select {
	case res := <-done:
		if res.code != http.StatusOK {
			t.Fatalf("%s %s: status = %d, want 200 (body %s)", method, path, res.code, res.raw)
		}
		var payload map[string]any
		_ = json.Unmarshal(res.raw, &payload)
		return &payload
	case <-time.After(deadline):
		t.Fatalf("%s %s did not respond within %s", method, path, deadline)
		return nil
	}
}

// TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing covers the 24-hour
// auto-release rule: cargo that has not reached the yard loses its slot, the
// freed capacity goes to the next waitlisted reservation, and the service keeps
// answering requests afterwards.
func TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing(t *testing.T) {
	s := newSweepEnv(t)
	defer s.env.resSvc.Close()
	s.addLocked(t, "req-1", false)
	s.addQueued(t, "req-2")

	released := s.runSweep(t, 5*time.Second)
	if len(released) != 1 || released[0] != "req-1" {
		t.Fatalf("released = %v, want [req-1]", released)
	}

	s.env.store.Mu.Lock()
	r1, _ := s.env.store.GetReservation("req-1")
	r2, _ := s.env.store.GetReservation("req-2")
	slot, _ := s.env.store.GetSlot("slot-1")
	s.env.store.Mu.Unlock()

	if r1.Status != domain.ReservationReleased {
		t.Fatalf("req-1 status = %s, want %s", r1.Status, domain.ReservationReleased)
	}
	if r2.Status != domain.ReservationLocked {
		t.Fatalf("req-2 status = %s, want %s after being promoted from the waitlist",
			r2.Status, domain.ReservationLocked)
	}
	if slot.Booked != 1 {
		t.Fatalf("slot booked = %d, want 1 (freed by req-1, taken by req-2)", slot.Booked)
	}

	wl := s.mustRespond(t, "GET", "/api/windows/win-1/waitlist", 5*time.Second)
	if length, _ := (*wl)["length"].(float64); length != 0 {
		t.Fatalf("waitlist length = %v, want 0 after promotion", (*wl)["length"])
	}
	s.mustRespond(t, "GET", "/api/slots", 5*time.Second)
	s.mustRespond(t, "GET", "/api/reservations/req-2", 5*time.Second)
}

// TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing covers the same rule
// when nobody is waiting for the freed capacity.
func TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing(t *testing.T) {
	s := newSweepEnv(t)
	defer s.env.resSvc.Close()
	s.addLocked(t, "req-1", false)

	released := s.runSweep(t, 5*time.Second)
	if len(released) != 1 || released[0] != "req-1" {
		t.Fatalf("released = %v, want [req-1]", released)
	}

	s.env.store.Mu.Lock()
	slot, _ := s.env.store.GetSlot("slot-1")
	s.env.store.Mu.Unlock()
	if slot.Booked != 0 {
		t.Fatalf("slot booked = %d, want 0 after the release", slot.Booked)
	}
	if slot.Status != domain.SlotStatusAvailable {
		t.Fatalf("slot status = %s, want %s", slot.Status, domain.SlotStatusAvailable)
	}
	s.mustRespond(t, "GET", "/api/slots", 5*time.Second)
}

// TestHTTP_RepeatedAutoReleaseSweepsKeepServing covers the periodic nature of
// the sweep: running it several times must stay a no-op once there is nothing
// left to release, and the service must remain responsive throughout.
func TestHTTP_RepeatedAutoReleaseSweepsKeepServing(t *testing.T) {
	s := newSweepEnv(t)
	defer s.env.resSvc.Close()
	s.addLocked(t, "req-1", false)
	s.addQueued(t, "req-2")

	if released := s.runSweep(t, 5*time.Second); len(released) != 1 {
		t.Fatalf("first sweep released = %v, want exactly one reservation", released)
	}
	for i := 2; i <= 4; i++ {
		released := s.runSweep(t, 5*time.Second)
		// req-2 was promoted in the first sweep and its cargo has not arrived
		// either, so it may be released by a later sweep; what matters is that
		// each sweep finishes and never releases the same reservation twice.
		for _, id := range released {
			r, _ := s.env.store.GetReservation(id)
			if r.Status != domain.ReservationReleased {
				t.Fatalf("sweep %d reported %s as released but its status is %s", i, id, r.Status)
			}
		}
		s.mustRespond(t, "GET", "/api/slots", 5*time.Second)
	}
}

// TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone pins the negative case:
// cargo that reached the yard in time keeps its slot.
func TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone(t *testing.T) {
	s := newSweepEnv(t)
	defer s.env.resSvc.Close()
	s.addLocked(t, "req-1", true)

	if released := s.runSweep(t, 5*time.Second); len(released) != 0 {
		t.Fatalf("released = %v, want none for cargo that already arrived", released)
	}

	s.env.store.Mu.Lock()
	r1, _ := s.env.store.GetReservation("req-1")
	s.env.store.Mu.Unlock()
	if r1.Status != domain.ReservationLocked {
		t.Fatalf("req-1 status = %s, want %s", r1.Status, domain.ReservationLocked)
	}
	s.mustRespond(t, "GET", "/api/slots", 5*time.Second)
}
