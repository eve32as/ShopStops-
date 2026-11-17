package httpapi

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "os"
 "sync"
 "testing"
 "time"

 "github.com/jackc/pgx/v5"
)

// Opt-in disposable database only. Tests actual commits and concurrent requests;
// never migrates a database and never contacts a payment provider.
func TestShopperPostgresIntegration(t *testing.T) {
 dsn:=os.Getenv("SHOPSTOPS_SHOPPER_TEST_DSN"); if dsn=="" { t.Skip("disposable PostGIS DSN not configured") }
 config,err:=pgx.ParseConfig(dsn)
 if err!=nil || (config.Host!="localhost" && config.Host!="127.0.0.1") || config.Database!="shopstops_ci" || config.User!="shopstops_ci" { t.Fatal("shopper integration requires local disposable shopstops_ci database/user") }
 db,err:=sql.Open("pgx",dsn); if err!=nil { t.Fatal(err) }; defer db.Close()
 db.SetMaxOpenConns(10)
 ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second); defer cancel()
 var order,store string
 err=db.QueryRowContext(ctx,`WITH zone AS (
 INSERT INTO delivery_zones(name,boundary) VALUES ('shopper Go fixture zone',ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id
 ), merchant AS (
 INSERT INTO merchants(name,merchant_type,stripe_account_id) VALUES ('shopper Go fixture','restaurant','acct_shopper_go_fixture') RETURNING id
 ), store AS (
 INSERT INTO store_locations(name,address,location,merchant_id) SELECT 'shopper Go fixture','fixture',ST_SetSRID(ST_MakePoint(-76.99,38.91),4326)::geography,id FROM merchant RETURNING id
 ), customer_order AS (
 INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
 SELECT 'shopper-go-customer',store.id,zone.id,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,'2026-11-15T12:00:00Z','2026-11-15T14:00:00Z',1,1200 FROM store CROSS JOIN zone RETURNING id,store_location_id
 ) SELECT id,store_location_id FROM customer_order`).Scan(&order,&store)
 if err!=nil { t.Fatal(err) }
 p:=NewPostgresRepository(db,311,89)
 start:=time.Date(2026,11,15,12,0,0,0,time.UTC)
 checkout,err:=p.CreateCheckout(ctx,checkoutRequest{AnchorStoreLocationID:store,OrderIDs:[]string{order},Dropoff:geoPoint{Latitude:38.91,Longitude:-76.98},DeliveryWindowStart:start,DeliveryWindowEnd:start.Add(2*time.Hour)},"shopper-go-customer","shopper-go-checkout")
 if err!=nil { t.Fatal(err) }
 if err=p.RecordPaymentIntent(ctx,checkout.ID,"pi_shopper_go_fixture",int64(checkout.TotalCents),"requires_payment_method"); err!=nil { t.Fatal(err) }
 key:="a1111111-1111-4111-8111-111111111111"
 otherKey:="a2222222-2222-4222-8222-222222222222"
 completeKey:="a3333333-3333-4333-8333-333333333333"
 request:=ShopperAssignment{OrderID:order,ShopperActor:"opaque_go_worker",CompletionAmountCents:501,JurisdictionRef:"US-DC"}
 first,err:=p.AssignShopperTask(ctx,"go-admin",key,request); if err!=nil { t.Fatal(err) }
 var assigned struct{ Status string; Earnings any; EligibilityStatus string }
 if err=json.Unmarshal(first,&assigned); err!=nil || assigned.Status!="assigned" || assigned.Earnings!=nil || assigned.EligibilityStatus!="pending_review" { t.Fatal(string(first),err) }
 retry,err:=p.AssignShopperTask(ctx,"go-admin",key,request); if err!=nil || string(first)!=string(retry) { t.Fatal("assignment retry",err) }
 changed:=request; changed.CompletionAmountCents++
 if _,err=p.AssignShopperTask(ctx,"go-admin",key,changed); !errors.Is(err,ErrConflict) { t.Fatal("amount conflict",err) }
 if _,err=p.AssignShopperTask(ctx,"other-admin",key,request); !errors.Is(err,ErrConflict) { t.Fatal("actor conflict",err) }
 if _,err=p.AssignShopperTask(ctx,"go-admin",otherKey,request); !errors.Is(err,ErrConflict) { t.Fatal("reassignment",err) }
 evidence:=ShopperCompletion{ItemsConfirmed:true,SubstitutionsConfirmed:true,ReceiptRef:"private_receipt",HandoffRef:"private_handoff"}
 // Concurrent identical completion retries must all return the same record.
 var wg sync.WaitGroup
 results:=make(chan json.RawMessage,8); errs:=make(chan error,8)
 for i:=0;i<8;i++ { wg.Add(1); go func(){ defer wg.Done(); raw,err:=p.CompleteShopperTask(ctx,"go-verifier",completeKey,key,evidence); results<-raw; errs<-err }() }
 wg.Wait(); close(results); close(errs)
 for err:=range errs { if err!=nil { t.Fatal("concurrent completion",err) } }
 var completed json.RawMessage
 for raw:=range results { if completed==nil { completed=raw }; if string(raw)!=string(completed) { t.Fatal("retry changed completion") } }
 var record struct{ Status string; Completion struct{ VerifiedBy,PaymentStatusAtCompletion string }; Earnings struct{ Kind,Status string; CompletionAmountCents int64 } }
 if err=json.Unmarshal(completed,&record); err!=nil || record.Status!="verified_completed" || record.Completion.VerifiedBy!="go-verifier" || record.Completion.PaymentStatusAtCompletion!="requires_payment_method" || record.Earnings.Status!="pending_review" || record.Earnings.Kind!="earned_per_order" || record.Earnings.CompletionAmountCents!=501 { t.Fatal(string(completed),err) }
 if _,err=p.CompleteShopperTask(ctx,"go-verifier",otherKey,key,evidence); !errors.Is(err,ErrConflict) { t.Fatal("new completion key",err) }
 evidence.ReceiptRef="different"
 if _,err=p.CompleteShopperTask(ctx,"go-verifier",completeKey,key,evidence); !errors.Is(err,ErrConflict) { t.Fatal("evidence conflict",err) }
 var earnings,audit int
 if err=db.QueryRowContext(ctx,`SELECT (SELECT count(*) FROM shopper_earnings WHERE order_id=$1::uuid),(SELECT count(*) FROM shopper_task_audit WHERE task_id=$2::uuid)`,order,key).Scan(&earnings,&audit); err!=nil || earnings!=1 || audit!=2 { t.Fatal(earnings,audit,err) }
 page,err:=p.ShopperTasks(ctx,1,100000); if err!=nil || page==nil || len(page)!=0 { t.Fatal("empty bounded page",err) }
 // Different keys racing for an unassigned order cannot create reassignment
 // or multiple earnings. Exactly one assignment and completion must commit.
 var raceOrder string
 err=db.QueryRowContext(ctx,`INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
 SELECT customer_ref,store_location_id,delivery_zone_id,dropoff_location,delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents FROM orders WHERE id=$1::uuid RETURNING id`,order).Scan(&raceOrder)
 if err!=nil { t.Fatal(err) }
 raceRequest:=request; raceRequest.OrderID=raceOrder
 raceKeys:=[]string{"b1111111-1111-4111-8111-111111111111","b2222222-2222-4222-8222-222222222222"}
 type raceResult struct { key string; err error }
 races:=make(chan raceResult,2)
 for _,raceKey:=range raceKeys { wg.Add(1); go func(k string){ defer wg.Done(); _,err:=p.AssignShopperTask(ctx,"go-admin",k,raceRequest); races<-raceResult{k,err} }(raceKey) }
 wg.Wait()
 winner:=""
 for i:=0;i<2;i++ { result:=<-races; if result.err==nil { if winner!="" { t.Fatal("two concurrent assignments committed") }; winner=result.key } else if !errors.Is(result.err,ErrConflict) { t.Fatal("assignment race",result.err) } }
 if winner=="" { t.Fatal("no assignment committed") }
 evidence.ReceiptRef="private_receipt"
 for _,raceKey:=range raceKeys { wg.Add(1); go func(k string){ defer wg.Done(); _,err:=p.CompleteShopperTask(ctx,"go-verifier",k,winner,evidence); races<-raceResult{k,err} }(raceKey) }
 wg.Wait()
 completedCount:=0
 for i:=0;i<2;i++ { result:=<-races; if result.err==nil { completedCount++ } else if !errors.Is(result.err,ErrConflict) { t.Fatal("completion race",result.err) } }
 if completedCount!=1 { t.Fatal("competing completion keys committed",completedCount) }
 if err=db.QueryRowContext(ctx,`SELECT count(*) FROM shopper_earnings WHERE order_id=$1::uuid`,raceOrder).Scan(&earnings); err!=nil || earnings!=1 { t.Fatal("duplicate race earnings",earnings,err) }
}