package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Fallback is per-invocation opt-in. Never send local credentials or targeted
// Guest requests. Never retry after a successful response or ambiguous failure.
func externalFallback(ctx context.Context, endpoint, model string, messages []protocol.Message) (string,error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname()=="" || u.User!=nil || u.RawQuery!="" || u.Fragment!="" {
		return "",errors.New("fallback endpoint must be an explicit HTTPS API base URL without credentials, query or fragment")
	}
	key := os.Getenv("NODEXLM_FALLBACK_API_KEY")
	if key=="" || model=="" { return "",errors.New("fallback requires --fallback-model and NODEXLM_FALLBACK_API_KEY") }
	payload, err := json.Marshal(map[string]any{"model":model,"messages":messages,"max_tokens":512,"stream":false})
	if err != nil { return "",err }
	req, err := http.NewRequestWithContext(ctx,http.MethodPost,strings.TrimRight(u.String(),"/")+"/chat/completions",bytes.NewReader(payload))
	if err != nil { return "",err }
	req.Header.Set("Authorization","Bearer "+key)
	req.Header.Set("Content-Type","application/json")
	client := &http.Client{Timeout:2*time.Minute,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	resp, err := client.Do(req)
	if err != nil { return "",errors.New("external inference request failed") }
	defer resp.Body.Close()
	if resp.StatusCode<200 || resp.StatusCode>=300 { return "",fmt.Errorf("external inference returned HTTP %d",resp.StatusCode) }
	var out chatOutput
	if err:=json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(&out);err!=nil{return "",errors.New("invalid external inference response")}
	if len(out.Choices)==0{return "",errors.New("empty external inference response")}
	return out.Choices[0].Message.Content,nil
}
