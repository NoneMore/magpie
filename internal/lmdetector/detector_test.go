package lmdetector

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "reflect"
 "testing"
)

func TestOptionsRejectsGroupsAndBadBoundaries(t *testing.T) {
 cases:=[]struct{name string; options Options; wantErr bool}{
  {"defaults",Options{Model:"one/a"},false},
  {"chat",Options{Model:"one/a",Format:"chat",Stream:true,Strict:true,Count:3},false},
  {"routing group",Options{Model:"group/test"},true},
  {"missing provider",Options{Model:"no-slash"},true},
  {"too many requests",Options{Model:"one/a",Count:4},true},
  {"too many rounds",Options{Model:"one/a",Repeat:21},true},
  {"strict partial",Options{Model:"one/a",Count:2,Strict:true},true},
  {"bad timeout",Options{Model:"one/a",Timeout:601},true},
  {"bad format",Options{Model:"one/a",Format:"messages"},true},
 }
 for _,tc:=range cases {
  t.Run(tc.name,func(t *testing.T){
   err:=tc.options.check()
   if (err!=nil)!=tc.wantErr{t.Fatalf("check = %v; want error %t",err,tc.wantErr)}
  })
 }
}

func TestModelsOnlyOffersDirectGatewayIDs(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.URL.Path!="/v1/models"||r.Header.Get("Authorization")!="Bearer test-key" {
   t.Errorf("unexpected Gateway request: %s / %s",r.URL.Path,r.Header.Get("Authorization"))
  }
  _=json.NewEncoder(w).Encode(map[string]any{"data":[]map[string]string{
   {"id":"beta/m2"},{"id":"group/choice"},{"id":"alpha/m1"},{"id":"alpha/m1"},{"id":"routing/slot"},
  }})
 }))
 defer server.Close()
 t.Setenv("MAGPIE_FPD_GATEWAY_URL",server.URL+"/v1")
 t.Setenv("MAGPIE_FPD_GATEWAY_KEY","test-key")
 got,err:=Models(context.Background())
 if err!=nil{t.Fatal(err)}
 want:=[]string{"alpha/m1","beta/m2"}
 if !reflect.DeepEqual(got,want){t.Fatalf("models=%q, want %q",got,want)}
}

func TestNativeRoutesRejectWhenDisabled(t *testing.T) {
 Disable()
 mux:=http.NewServeMux()
 Routes(mux)
 for _,path:=range []string{"models","start","status","cancel"}{
  req:=httptest.NewRequest("POST","/api/lm-detector/"+path,nil)
  response:=httptest.NewRecorder()
  mux.ServeHTTP(response,req)
  if response.Code!=http.StatusServiceUnavailable {
   t.Errorf("%s status=%d; want 503",path,response.Code)
  }
 }
}
