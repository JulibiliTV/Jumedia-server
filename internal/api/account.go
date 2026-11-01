package api

func (s *Server) registerAccount() {
	s.handle("/login/exit/v2", s.accountLogout)
	s.handle("/x/passport-tv-login/qrcode/auth_code", s.accountAuthCode)
	s.handle("/x/passport-tv-login/qrcode/poll", s.accountPoll)
	s.handle("/x/passport-tv-login/h5/qrcode/scan", s.accountScan)
	s.handle("/x/passport-tv-login/h5/qrcode/confirm", s.accountConfirm)
	s.handle("/api/v2/oauth2/refresh_token", s.accountRefresh)
	s.handle("/x/passport-login/web/login", s.accountLogin)
	s.handle("/x/passport-login/web/register", s.accountRegister)
	s.handle("/x/passport-login/web/password/update", s.accountPassword)
	s.handle("/x/member/web/update", s.accountProfile)
}
