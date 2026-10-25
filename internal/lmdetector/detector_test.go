package lmdetector

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "reflect"
 "io"
 "fmt"
 "strings"
 "time"

 "github.com/yetone/magpie/internal/testenv"
 "testing"
)

func TestMain(m *testing.M) {
 testenv.Offline()
 testenv.Main(m)
}

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

func TestNativeSamplingThroughGateway(t *testing.T) {
 numbers:=strings.TrimSuffix(strings.Repeat("199, ",300),", ")
 mock:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  switch {
  case r.Method=="GET" && r.URL.Path=="/v1/models":
   _=json.NewEncoder(w).Encode(map[string]any{"data":[]map[string]string{{"id":"test/fixture"}}})
  case r.Method=="POST" && (r.URL.Path=="/v1/chat/completions"||r.URL.Path=="/v1/responses"):
   body,err:=io.ReadAll(r.Body);if err!=nil{t.Error(err);return}
   var request struct{Stream bool `json:"stream"`}
   if err:=json.Unmarshal(body,&request);err!=nil{t.Error(err);return}
   isChat:=r.URL.Path=="/v1/chat/completions"
   if request.Stream {
    w.Header().Set("Content-Type","text/event-stream")
    if isChat {
     fmt.Fprintf(w,"data: %s\n\n",mustJSON(map[string]any{"choices":[]any{map[string]any{"delta":map[string]string{"content":numbers},"finish_reason":nil}}}))
     fmt.Fprint(w,"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
    } else {
     fmt.Fprintf(w,"data: %s\n\n",mustJSON(map[string]any{"type":"response.output_text.delta","delta":numbers}))
     fmt.Fprint(w,"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
    }
   } else if isChat {
    _=json.NewEncoder(w).Encode(map[string]any{"choices":[]any{map[string]any{"finish_reason":"stop","message":map[string]string{"content":numbers}}}})
   } else {
    _=json.NewEncoder(w).Encode(map[string]any{"status":"completed","output":[]any{map[string]any{"type":"message","content":[]any{map[string]string{"type":"output_text","text":numbers}}}}})
   }
  default:http.NotFound(w,r)
  }
 }))
 defer mock.Close()
 t.Setenv("MAGPIE_FPD_GATEWAY_URL",mock.URL+"/v1")
 t.Setenv("MAGPIE_FPD_GATEWAY_KEY","test-key")
 Enable()
 defer Disable()
 for _,format:=range []string{"chat","responses"} {
  for _,stream:=range []bool{false,true} {
   t.Run(fmt.Sprintf("%s/stream=%t",format,stream),func(t *testing.T){
    start,err:=Start(context.Background(),Options{Model:"test/fixture",Format:format,Stream:stream,Count:1,Parallel:1,Repeat:1})
    if err!=nil{t.Fatal(err)}
    deadline:=time.Now().Add(10*time.Second)
    for {
     job,err:=Status(start.JobID)
     if err!=nil{t.Fatal(err)}
     if job.State!="running" {
      if job.State!="done"||job.Completed!=1||len(job.Rounds)!=1||job.Rounds[0].Analysis==nil||job.Rounds[0].Analysis.Decision!="not_model" {
       t.Fatalf("unexpected native detection: %+v",job)
      }
      return
     }
     if time.Now().After(deadline){t.Fatal("native detector job did not finish")}
     time.Sleep(25*time.Millisecond)
    }
   })
  }
 }
}
func mustJSON(v any)string {b,_:=json.Marshal(v);return string(b)}
