package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ShopperOrder struct {
	ID              string              `json:"id"`
	StoreLocationID string              `json:"storeLocationId"`
	StoreName       string              `json:"storeName"`
	Status          string              `json:"status"`
	Items           []MerchantOrderItem `json:"items"`
}

type ShopperFulfillmentRepository interface {
	ShopperOrders(context.Context, string) ([]ShopperOrder, error)
	ClaimShopperOrder(context.Context, string, string) error
	PackShopperOrder(context.Context, string, string, []byte) error
	VerifyShopperCustody(context.Context, string, string, string, string, []byte) error
}

type custodyPINRequest struct {
	OrderID string `json:"orderId"`
	PIN     string `json:"pin"`
}

func (s *Server) listShopperOrders(w http.ResponseWriter, r *http.Request) {
	shopperRef, ok := s.authorizedActorRole(w, r, "shopper")
	if !ok {
		return
	}
	repository, ok := s.repo.(ShopperFulfillmentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "shopper fulfillment is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	orders, err := repository.ShopperOrders(ctx, shopperRef)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *Server) claimShopperOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("orderId")
	if !isUUID(orderID) {
		writeError(w, http.StatusBadRequest, "valid order ID is required")
		return
	}
	shopperRef, ok := s.authorizedActorRole(w, r, "shopper")
	if !ok {
		return
	}
	repository, ok := s.repo.(ShopperFulfillmentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "shopper fulfillment is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := repository.ClaimShopperOrder(ctx, orderID, shopperRef); err != nil {
		s.writeShopperOrderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": orderID, "status": "claimed"})
}

func (s *Server) packShopperOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("orderId")
	if !isUUID(orderID) {
		writeError(w, http.StatusBadRequest, "valid order ID is required")
		return
	}
	shopperRef, ok := s.authorizedActorRole(w, r, "shopper")
	if !ok {
		return
	}
	repository, ok := s.repo.(ShopperFulfillmentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "shopper fulfillment is unavailable")
		return
	}
	pin := custodyPIN(s.token, orderID)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := repository.PackShopperOrder(
		ctx, orderID, shopperRef, custodyPINDigest(s.token, orderID, pin),
	); err != nil {
		s.writeShopperOrderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id": orderID, "status": "packed", "custodyPIN": pin,
	})
}

func (s *Server) verifyShopperCustody(w http.ResponseWriter, r *http.Request) {
	routeID, stopID := r.PathValue("routeId"), r.PathValue("stopId")
	if !isUUID(routeID) || !isUUID(stopID) {
		writeError(w, http.StatusBadRequest, "valid route and stop IDs are required")
		return
	}
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024)
	var request custodyPINRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid custody PIN request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.OrderID) || len(request.PIN) != 4 ||
		request.PIN[0] < '0' || request.PIN[0] > '9' ||
		request.PIN[1] < '0' || request.PIN[1] > '9' ||
		request.PIN[2] < '0' || request.PIN[2] > '9' ||
		request.PIN[3] < '0' || request.PIN[3] > '9' {
		writeError(w, http.StatusBadRequest, "order ID and four-digit PIN are required")
		return
	}
	repository, ok := s.repo.(ShopperFulfillmentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "shopper fulfillment is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := repository.VerifyShopperCustody(
		ctx, routeID, stopID, request.OrderID, driverRef,
		custodyPINDigest(s.token, request.OrderID, request.PIN),
	); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, "order or pickup stop not found")
		case errors.Is(err, ErrForbidden):
			writeError(w, http.StatusForbidden, "route is not assigned to this driver")
		case errors.Is(err, ErrConflict):
			writeError(w, http.StatusConflict, "custody PIN is invalid or the pickup is not ready")
		default:
			s.databaseError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"orderId": request.OrderID, "status": "custody_verified",
	})
}

func (s *Server) writeShopperOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "order not found")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "order is assigned to another shopper")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "order is no longer available for shopper fulfillment")
	default:
		s.databaseError(w, err)
	}
}

func custodyPIN(secret, orderID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("shopstops-custody-pin:" + orderID))
	value := binary.BigEndian.Uint32(mac.Sum(nil)[:4]) % 10000
	return fmt.Sprintf("%04d", value)
}

func custodyPINDigest(secret, orderID, pin string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("shopstops-custody-check:" + orderID + ":" + pin))
	return mac.Sum(nil)
}

func (p *PostgresRepository) ShopperOrders(
	ctx context.Context, shopperRef string,
) ([]ShopperOrder, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT o.id::text, s.id::text, s.name, o.state,
		       COALESCE(jsonb_agg(jsonb_build_object(
		           'name', li.item_name, 'quantity', li.quantity,
		           'unitPriceCents', li.unit_price_cents
		       ) ORDER BY li.id) FILTER (WHERE li.id IS NOT NULL), '[]'::jsonb)
		FROM orders o
		JOIN store_locations s ON s.id = o.store_location_id
		JOIN checkout_orders co ON co.order_id = o.id
		JOIN unified_checkouts c ON c.id = co.checkout_id AND c.status IN ('paid', 'dispatching', 'in_progress')
		LEFT JOIN order_line_items li ON li.order_id = o.id
		WHERE o.state = 'ready'
		  AND (o.fulfillment_mode = 'driver'
		       OR (o.fulfillment_mode = 'in_store_shopper'
		           AND o.in_store_shopper_ref = $1 AND o.packed_at IS NULL))
		GROUP BY o.id, s.id, s.name
		ORDER BY o.ready_at, o.id
		LIMIT 100`, shopperRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]ShopperOrder, 0)
	for rows.Next() {
		var order ShopperOrder
		var items []byte
		if err := rows.Scan(
			&order.ID, &order.StoreLocationID, &order.StoreName, &order.Status, &items,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(items, &order.Items); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (p *PostgresRepository) ClaimShopperOrder(
	ctx context.Context, orderID, shopperRef string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE orders
		SET fulfillment_mode = 'in_store_shopper',
		    in_store_shopper_ref = $2,
		    updated_at = now()
		WHERE id = $1 AND state = 'ready'
		  AND (fulfillment_mode = 'driver'
		       OR (fulfillment_mode = 'in_store_shopper'
		           AND in_store_shopper_ref = $2 AND packed_at IS NULL))`,
		orderID, shopperRef)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	var owner string
	if err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(in_store_shopper_ref, '') FROM orders WHERE id = $1`, orderID,
	).Scan(&owner); err != nil {
		return err
	}
	if owner != "" && owner != shopperRef {
		return ErrForbidden
	}
	return ErrConflict
}

func (p *PostgresRepository) PackShopperOrder(
	ctx context.Context, orderID, shopperRef string, pinHash []byte,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE orders
		SET packed_at = COALESCE(packed_at, now()),
		    custody_pin_hash = COALESCE(custody_pin_hash, $3),
		    updated_at = now()
		WHERE id = $1 AND state = 'ready'
		  AND fulfillment_mode = 'in_store_shopper'
		  AND in_store_shopper_ref = $2`,
		orderID, shopperRef, pinHash)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	var owner string
	if err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(in_store_shopper_ref, '') FROM orders WHERE id = $1`, orderID,
	).Scan(&owner); err != nil {
		return err
	}
	if owner != shopperRef {
		return ErrForbidden
	}
	return ErrConflict
}

func (p *PostgresRepository) VerifyShopperCustody(
	ctx context.Context, routeID, stopID, orderID, driverRef string, pinHash []byte,
) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assigned, eligible bool
	if err := tx.QueryRowContext(ctx, `
		SELECT r.driver_ref = $3,
		       s.status = 'arrived' AND s.stop_type = 'pickup'
		       AND r.status = 'in_progress'
		       AND NOT EXISTS (
		           SELECT 1 FROM unified_route_stops previous
		           WHERE previous.route_id = r.id
		             AND previous.sequence < s.sequence
		             AND previous.status <> 'completed'
		       )
		FROM unified_delivery_routes r
		JOIN unified_route_stops s ON s.route_id = r.id
		WHERE r.id = $1 AND s.id = $2
		FOR UPDATE OF r, s`, routeID, stopID, driverRef,
	).Scan(&assigned, &eligible); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !assigned {
		return ErrForbidden
	}
	if !eligible {
		return ErrConflict
	}
	var storedHash []byte
	var storedOrderID string
	var attempts int
	err = tx.QueryRowContext(ctx, `
		SELECT o.id::text, o.custody_pin_hash, o.custody_pin_attempts
		FROM unified_delivery_routes r
		JOIN checkout_orders co ON co.checkout_id = r.checkout_id
		JOIN orders o ON o.id = co.order_id
		JOIN unified_route_stops s
		  ON s.route_id = r.id AND s.store_location_id = o.store_location_id
		WHERE r.id = $1 AND s.id = $2 AND o.id = $3
		  AND o.fulfillment_mode = 'in_store_shopper'
		  AND o.packed_at IS NOT NULL AND o.custody_verified_at IS NULL
		  AND o.custody_pin_attempts < 5
		FOR UPDATE OF o`, routeID, stopID, orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	valid := subtle.ConstantTimeCompare(storedHash, pinHash) == 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET custody_pin_attempts = $2 + 1,
		    custody_verified_at = CASE WHEN $3 THEN now() ELSE NULL END,
		    updated_at = now()
		WHERE id = $1`, storedOrderID, attempts, valid); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}
