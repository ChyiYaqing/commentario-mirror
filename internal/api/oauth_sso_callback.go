package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/go-openapi/strfmt"
	"gitlab.com/comentario/comentario/internal/util"
	"net/http"
)

func ssoCallbackHandler(w http.ResponseWriter, r *http.Request) {
	payloadHex := r.FormValue("payload")
	signature := r.FormValue("hmac")

	payloadBytes, err := hex.DecodeString(payloadHex)
	if err != nil {
		_, _ = fmt.Fprintf(w, "Error: invalid JSON payload hex encoding: %s\n", err.Error())
		return
	}

	signatureBytes, err := hex.DecodeString(signature)
	if err != nil {
		_, _ = fmt.Fprintf(w, "Error: invalid HMAC signature hex encoding: %s\n", err.Error())
		return
	}

	payload := ssoPayload{}
	err = json.Unmarshal(payloadBytes, &payload)
	if err != nil {
		_, _ = fmt.Fprintf(w, "Error: cannot unmarshal JSON payload: %s\n", err.Error())
		return
	}

	if payload.Token == "" || payload.Email == "" || payload.Name == "" {
		_, _ = fmt.Fprintf(w, "Error: %s\n", util.ErrorMissingField.Error())
		return
	}

	if payload.Link == "" {
		payload.Link = "undefined"
	}

	if payload.Photo == "" {
		payload.Photo = "undefined"
	}

	domain, commenterToken, err := ssoTokenExtract(payload.Token)
	if err != nil {
		_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		return
	}

	d, err := domainGet(domain)
	if err != nil {
		if err == util.ErrorNoSuchDomain {
			_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		} else {
			logger.Errorf("cannot get domain for SSO: %v", err)
			_, _ = fmt.Fprintf(w, "Error: %s\n", util.ErrorInternal.Error())
		}
		return
	}

	if d.SsoSecret == "" || d.SsoUrl == "" {
		_, _ = fmt.Fprintf(w, "Error: %s\n", util.ErrorMissingConfig.Error())
		return
	}

	key, err := hex.DecodeString(d.SsoSecret)
	if err != nil {
		logger.Errorf("cannot decode SSO secret as hex: %v", err)
		_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		return
	}

	h := hmac.New(sha256.New, key)
	h.Write(payloadBytes)
	expectedSignatureBytes := h.Sum(nil)
	if !hmac.Equal(expectedSignatureBytes, signatureBytes) {
		_, _ = fmt.Fprintf(w, "Error: HMAC signature verification failed\n")
		return
	}

	_, err = commenterGetByCommenterToken(commenterToken)
	if err != nil && err != util.ErrorNoSuchToken {
		_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		return
	}

	c, err := commenterGetByEmail("sso:"+domain, strfmt.Email(payload.Email))
	if err != nil && err != util.ErrorNoSuchCommenter {
		_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		return
	}

	var commenterHex string

	if err == util.ErrorNoSuchCommenter {
		commenterHex, err = commenterNew(strfmt.Email(payload.Email), payload.Name, payload.Link, payload.Photo, "sso:"+domain, "")
		if err != nil {
			_, _ = fmt.Fprintf(w, "Error: %s", err.Error())
			return
		}
	} else {
		if err = commenterUpdate(c.CommenterHex, payload.Email, payload.Name, payload.Link, payload.Photo, "sso:"+domain); err != nil {
			logger.Warningf("cannot update commenter: %s", err)
			// not a serious enough to exit with an error
		}

		commenterHex = c.CommenterHex
	}

	if err = commenterSessionUpdate(commenterToken, commenterHex); err != nil {
		_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
		return
	}

	_, _ = fmt.Fprintf(w, "<html><script>window.parent.close()</script></html>")
}
