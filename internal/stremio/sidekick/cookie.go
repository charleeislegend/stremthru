package stremio_sidekick

import (
	"net/http"
	"net/url"
	"time"
)

type AdminCookieValue struct {
	url.Values
	IsExpired bool
}

func (cv *AdminCookieValue) User() string {
	return cv.Get("user")
}

func (cv *AdminCookieValue) Pass() string {
	return cv.Get("pass")
}

const ADMIN_COOKIE_NAME = "stremio.auth.stremthru.admin"
const ADMIN_COOKIE_PATH = "/stremio/"

func setAdminCookie(w http.ResponseWriter, user string, pass string) {
	value := &url.Values{
		"user": []string{user},
		"pass": []string{pass},
	}
	cookie := &http.Cookie{
		Name:     ADMIN_COOKIE_NAME,
		Value:    value.Encode(),
		HttpOnly: true,
		Path:     ADMIN_COOKIE_PATH,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, cookie)
}

func unsetAdminCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    ADMIN_COOKIE_NAME,
		Expires: time.Unix(0, 0),
		Path:    ADMIN_COOKIE_PATH,
	})
}

func getAdminCookieValue(w http.ResponseWriter, r *http.Request) (*AdminCookieValue, error) {
	cookie, err := r.Cookie(ADMIN_COOKIE_NAME)
	value := &AdminCookieValue{}
	if err != nil {
		if err != http.ErrNoCookie {
			return value, err
		}
		value.IsExpired = true
		return value, nil
	}

	v, err := url.ParseQuery(cookie.Value)
	if err != nil {
		LogError(r, "failed to parse cookie value", err)
		unsetAdminCookie(w)
		value.IsExpired = true
		return value, nil
	}
	value.Values = v
	return value, nil
}
