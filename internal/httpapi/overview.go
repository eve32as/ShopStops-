package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// AdminOverviewRepository is optional so existing repositories remain compatible.
type AdminOverviewRepository interface {
	AdminOverview(context.Context) (json.RawMessage, error)
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	repository, ok := s.repo.(AdminOverviewRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "admin overview unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := repository.AdminOverview(ctx)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	var payload any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeError(w, http.StatusServiceUnavailable, "invalid admin overview response")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (p *PostgresRepository) AdminOverview(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := p.db.QueryRowContext(ctx, `
		SELECT jsonb_build_object(
			'kind','admin_overview',
			'generatedAt',to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
			'checkouts',COALESCE((SELECT jsonb_object_agg(status,count) FROM (
				SELECT status,count(*)::text FROM unified_checkouts GROUP BY status
			) s),'{}'::jsonb),
			'refunds',jsonb_build_object(
				'pending',(SELECT count(*) FROM checkout_refunds WHERE status='pending'),
				'succeeded',(SELECT count(*) FROM checkout_refunds WHERE status='succeeded'),
				'failed',(SELECT count(*) FROM checkout_refunds WHERE status='failed'),
				'canceled',(SELECT count(*) FROM checkout_refunds WHERE status='canceled')
			),
			'manualCosts',jsonb_build_object(
				'recordCount',(SELECT count(*) FROM finance_costs),
				'totalCents',(SELECT COALESCE(sum(amount_cents),0)::text FROM finance_costs)
			),
			'shopperTasks',jsonb_build_object(
				'assigned',(SELECT count(*) FROM shopper_tasks t WHERE NOT EXISTS (SELECT 1 FROM shopper_completions c WHERE c.task_id=t.id)),
				'verifiedCompleted',(SELECT count(*) FROM shopper_completions),
				'pendingReviewEarnings',(SELECT count(*) FROM shopper_earnings WHERE status='pending_review')
			)
		)`).Scan(&raw)
	return raw, err
}

type CheckoutRefundStatusRepository interface {
	CheckoutRefundStatus(context.Context, string) (json.RawMessage, error)
}

func (s *Server) checkoutRefundStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	checkoutID := r.PathValue("checkoutId")
	if !isUUID(checkoutID) {
		writeError(w, http.StatusBadRequest, "invalid checkoutId")
		return
	}
	repository, ok := s.repo.(CheckoutRefundStatusRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "refund status unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := repository.CheckoutRefundStatus(ctx, checkoutID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "checkout refund not found")
			return
		}
		s.databaseError(w, err)
		return
	}
	var payload any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeError(w, http.StatusServiceUnavailable, "invalid refund status response")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (p *PostgresRepository) CheckoutRefundStatus(ctx context.Context, checkoutID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := p.db.QueryRowContext(ctx, `
		SELECT jsonb_build_object(
			'kind','checkout_refund_status','checkoutId',$1::text,
			'refunds',COALESCE((
				SELECT jsonb_agg(jsonb_build_object(
					'id',id,'stripeRefundId',stripe_refund_id,'status',status,
					'amountCents',amount_cents,'reason',reason,
					'attemptedAt',to_char(attempted_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
					'updatedAt',to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
					ORDER BY created_at,id)
				FROM checkout_refunds WHERE checkout_id=$1::uuid
			),'[]'::jsonb)
		WHERE EXISTS (SELECT 1 FROM unified_checkouts WHERE id=$1::uuid)`, checkoutID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ErrNotFound
	}
	return raw, nil
}
