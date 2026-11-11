package identity

import "strings"

type User struct {
	ID, Phone, Nickname, Role string
	Enabled                   bool
}

func RoleFor(surface string) string {
	if surface == "admin" {
		return "admin"
	}
	return "member"
}

func (u User) CanAccess(surface string) bool {
	return (surface == "public" || surface == "admin") && u.Enabled && u.Role == RoleFor(surface)
}

func NormalizeLogin(phone string) string { return strings.TrimSpace(phone) }
