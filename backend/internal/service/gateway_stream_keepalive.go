package service

import "time"

const defaultKiroStreamKeepalive = 25 * time.Second

func (s *GatewayService) streamKeepaliveIntervalForAccount(account *Account) time.Duration {
	if account != nil && account.IsKiro() {
		if s != nil && s.cfg != nil && s.cfg.Gateway.KiroStreamKeepaliveInterval > 0 {
			return time.Duration(s.cfg.Gateway.KiroStreamKeepaliveInterval) * time.Second
		}
		return defaultKiroStreamKeepalive
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		return time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	return 0
}
