package httpapi

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "log/slog"
 "net/http"
 "strings"
 "testing"

 "github.com/jackc/pgx/v5/pgconn"
)

const shopperAssignmentBody = `{"orderId":"11111111-1111-4111-8111-111111111111","shopperActor":"opaque_worker_1","completionAmountCents":501,"jurisdictionRef":"US-DC"}`
const shopperCompletionBody = `{"itemsConfirmed":true,"substitutionsConfirmed":true,"receiptRef":"receipt_1","handoffRef":"handoff_1"}`
const shopperCompletionPath = "/admin/shopper/tasks/11111111-1111-4111-8111-111111111111/complete"

type fakeShopperRepository struct {
 fakeRepository
 calls int
 actor,key,id string
 assignment ShopperAssignment
 completion ShopperCompletion
 limit,offset int
 err error
}
func (f *fakeShopperRepository) AssignShopperTask(_ context.Context,actor,key string,r ShopperAssignment) (json.RawMessage,error) {
 f.calls++; f.actor,f.key,f.assignment=actor,key,r
 return json.RawMessage(`{"status":"assigned","earnings":null}`),f.err
}
func (f *fakeShopperRepository) CompleteShopperTask(_ context.Context,actor,key,id string,r ShopperCompletion) (json.RawMessage,error) {
 f.calls++; f.actor,f.key,f.id,f.completion=actor,key,id,r
 return json.RawMessage(`{"status":"verified_completed","earnings":{"kind":"earned_per_order","status":"pending_review","completionAmountCents":501}}`),f.err
}
func (f *fakeShopperRepository) ShopperTasks(_ context.Context,limit,offset int) ([]json.RawMessage,error) {
 f.calls++; f.limit,f.offset=limit,offset
 return []json.RawMessage{},f.err
}
func shopperHandler(f *fakeShopperRepository) http.Handler { return New(f,strings.Repeat("t",32),slog.New(slog.NewTextHandler(io.Discard,nil))) }

func TestShopperAdminOnly(t *testing.T) {
 f:=&fakeShopperRepository{}; h:=shopperHandler(f)
 for _,op:=range []struct{method,path,body string}{{"POST","/admin/shopper/tasks",shopperAssignmentBody},{"GET","/admin/shopper/tasks",""},{"POST",shopperCompletionPath,shopperCompletionBody}} {
  for _,tc:=range []struct{actor,role string; auth bool; code int}{{"admin","admin",false,401},{"","admin",true,400},{strings.Repeat("a",201),"admin",true,400},{"customer","customer",true,403},{"merchant","merchant",true,403},{"driver","driver",true,403},{"shopper","shopper",true,403}} {
   w:=financeRequest(h,op.method,op.path,op.body,tc.actor,tc.role,financeKey,tc.auth)
   if w.Code!=tc.code { t.Fatalf("%s %s %d %s",op.path,tc.role,w.Code,w.Body) }
  }
 }
 if f.calls!=0 { t.Fatal("unauthorized repository access") }
}
func TestShopperStrictInputsAndNoAssignmentEarnings(t *testing.T) {
 f:=&fakeShopperRepository{}; h:=shopperHandler(f)
 for _,body:=range []string{"", "null", "[]", "{}", strings.Replace(shopperAssignmentBody,"501","0",1),strings.Replace(shopperAssignmentBody,"501","-1",1),strings.Replace(shopperAssignmentBody,"501","100000001",1),strings.Replace(shopperAssignmentBody,"501","1.2",1),strings.Replace(shopperAssignmentBody,"501","null",1),strings.Replace(shopperAssignmentBody,"opaque_worker_1","person@example.com",1),strings.Replace(shopperAssignmentBody,"US-DC",strings.Repeat("x",101),1),strings.Replace(shopperAssignmentBody,"11111111-1111-4111-8111-111111111111","bad",1),strings.TrimSuffix(shopperAssignmentBody,"}")+`,"verifiedBy":"client"}`,shopperAssignmentBody+` {}`,strings.Repeat(" ",8193)+shopperAssignmentBody} {
  w:=financeRequest(h,"POST","/admin/shopper/tasks",body,"admin","admin",financeKey,true)
  if w.Code!=400 { t.Fatalf("%d %s",w.Code,w.Body) }
 }
 for _,key:=range []string{"","not-a-uuid"} { if w:=financeRequest(h,"POST","/admin/shopper/tasks",shopperAssignmentBody,"admin","admin",key,true); w.Code!=400 { t.Fatal(w.Code) } }
 if f.calls!=0 { t.Fatal("invalid input reached repo") }
 w:=financeRequest(h,"POST","/admin/shopper/tasks",shopperAssignmentBody,"server-admin","admin",financeKey,true)
 if w.Code!=201 || f.actor!="server-admin" || f.key!=financeKey || f.assignment.CompletionAmountCents!=501 || !strings.Contains(w.Body.String(),`"earnings":null`) { t.Fatalf("%d %s %+v",w.Code,w.Body,f) }
 for _,body:=range []string{"{}",strings.Replace(shopperCompletionBody,"true","false",1),strings.Replace(shopperCompletionBody,`"substitutionsConfirmed":true`,`"substitutionsConfirmed":null`,1),strings.Replace(shopperCompletionBody,"receipt_1","https://example.com/proof",1),strings.Replace(shopperCompletionBody,"handoff_1","",1),strings.TrimSuffix(shopperCompletionBody,"}")+`,"completionAmountCents":999}`,strings.TrimSuffix(shopperCompletionBody,"}")+`,"verifiedBy":"client"}`,shopperCompletionBody+` {}`,strings.Repeat(" ",8193)+shopperCompletionBody} {
  if w:=financeRequest(h,"POST",shopperCompletionPath,body,"admin","admin",financeKey,true); w.Code!=400 { t.Fatalf("%d %s",w.Code,w.Body) }
 }
 if f.calls!=1 { t.Fatal("bad evidence reached repo") }
 w=financeRequest(h,"POST",shopperCompletionPath,shopperCompletionBody,"server-verifier","admin",financeKey,true)
 if w.Code!=200 || f.actor!="server-verifier" || f.id!=financeKey || !f.completion.ItemsConfirmed || !strings.Contains(w.Body.String(),`"status":"pending_review"`) { t.Fatalf("%d %s",w.Code,w.Body) }
}
func TestShopperPaginationAvailabilityAndErrors(t *testing.T) {
 f:=&fakeShopperRepository{}; h:=shopperHandler(f)
 for _,q:=range []string{"?limit=0","?limit=101","?limit=1.5","?offset=-1","?offset=100001","?offset=x"} {
  if w:=financeRequest(h,"GET","/admin/shopper/tasks"+q,"","admin","admin","",true); w.Code!=400 { t.Fatal(w.Code,q) }
 }
 w:=financeRequest(h,"GET","/admin/shopper/tasks?limit=1&offset=100000","","admin","admin","",true)
 if w.Code!=200 || f.limit!=1 || f.offset!=100000 || !strings.Contains(w.Body.String(),`"tasks":[]`) { t.Fatal(w.Body) }
 for _,op:=range []struct{method,path,body string}{{"POST","/admin/shopper/tasks",shopperAssignmentBody},{"GET","/admin/shopper/tasks",""},{"POST",shopperCompletionPath,shopperCompletionBody}} {
  if w:=financeRequest(testServer(),op.method,op.path,op.body,"admin","admin",financeKey,true); w.Code!=503 { t.Fatalf("optional repo %d %s",w.Code,w.Body) }
  for _,tc:=range []struct{err error; code int}{{ErrConflict,409},{ErrNotFound,404},{&pgconn.PgError{Code:"42P01"},503},{&pgconn.PgError{Code:"42883"},503},{&pgconn.PgError{Code:"22023"},400},{errors.New("internal"),500}} {
   f.err=tc.err; w:=financeRequest(h,op.method,op.path,op.body,"admin","admin",financeKey,true)
   if w.Code!=tc.code { t.Fatalf("%s %d %s",op.path,w.Code,w.Body) }
  }
 }
 for _,tc:=range []struct{code string; want error}{{"23505",ErrConflict},{"P0002",ErrNotFound}} {
  if !errors.Is(shopperDBError(&pgconn.PgError{Code:tc.code}),tc.want) { t.Fatal(tc.code) }
 }
}