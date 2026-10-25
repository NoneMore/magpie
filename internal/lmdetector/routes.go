package lmdetector

import (
 "encoding/json"
 "errors"
 "net/http"
)

func readJSON(r *http.Request, value any) error {
 r.Body = http.MaxBytesReader(nil, r.Body, 16<<10)
 d:=json.NewDecoder(r.Body)
 d.DisallowUnknownFields()
 return d.Decode(value)
}

func answer(w http.ResponseWriter, value any) {
 w.Header().Set("Content-Type","application/json; charset=utf-8")
 w.Header().Set("Cache-Control","no-store")
 _=json.NewEncoder(w).Encode(value)
}

func reject(w http.ResponseWriter,err error) {
 code:=http.StatusBadRequest
 if !Enabled(){code=http.StatusServiceUnavailable}
 http.Error(w,err.Error(),code)
}

// Routes registers management endpoints under Magpie's existing web/API
// authentication boundary; it never exposes the Gateway key to the Page.
func Routes(mux *http.ServeMux) {
 mux.HandleFunc("POST /api/lm-detector/models",func(w http.ResponseWriter,r *http.Request){
  if !Enabled(){reject(w,errors.New("model fingerprint plugin is disabled"));return}
  models,err:=Models(r.Context())
  if err!=nil{reject(w,err);return}
  answer(w,map[string]any{"models":models})
 })
 mux.HandleFunc("POST /api/lm-detector/start",func(w http.ResponseWriter,r *http.Request){
  if !Enabled(){reject(w,errors.New("model fingerprint plugin is disabled"));return}
  var opts Options
  if err:=readJSON(r,&opts);err!=nil{reject(w,err);return}
  job,err:=Start(r.Context(),opts)
  if err!=nil{reject(w,err);return}
  answer(w,job)
 })
 mux.HandleFunc("POST /api/lm-detector/status",func(w http.ResponseWriter,r *http.Request){
  if !Enabled(){reject(w,errors.New("model fingerprint plugin is disabled"));return}
  var in struct{JobID string `json:"jobId"`}
  if err:=readJSON(r,&in);err!=nil{reject(w,err);return}
  job,err:=Status(in.JobID)
  if err!=nil{reject(w,err);return}
  answer(w,job)
 })
 mux.HandleFunc("POST /api/lm-detector/cancel",func(w http.ResponseWriter,r *http.Request){
  if !Enabled(){reject(w,errors.New("model fingerprint plugin is disabled"));return}
  var in struct{JobID string `json:"jobId"`}
  if err:=readJSON(r,&in);err!=nil{reject(w,err);return}
  job,err:=Cancel(in.JobID)
  if err!=nil{reject(w,err);return}
  answer(w,job)
 })
}
