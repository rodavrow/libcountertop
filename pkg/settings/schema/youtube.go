package schema

import "slices"

const YouTubeAnonymous = "anonymous"

type YouTubeAccounts struct {
	Device   string           `json:"device,omitempty"`
	Chosen   string           `json:"chosen,omitempty"`
	Visitor  string           `json:"visitor,omitempty"`
	Accounts []YouTubeAccount `json:"accounts,omitempty"`
}

type YouTubeAccount struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Photo   string `json:"photo,omitempty"`
	Client  string `json:"client"`
	Secret  string `json:"secret"`
	Refresh string `json:"refresh"`
}

func (y YouTubeAccounts) Clone() YouTubeAccounts {
	y.Accounts = slices.Clone(y.Accounts)
	return y
}

func (y YouTubeAccounts) Find(id string) (YouTubeAccount, bool) {
	i := slices.IndexFunc(y.Accounts, func(a YouTubeAccount) bool { return a.ID == id })
	if i < 0 {
		return YouTubeAccount{}, false
	}
	return y.Accounts[i], true
}

func (y YouTubeAccounts) Anonymous() bool { return y.Chosen == YouTubeAnonymous }

func (y YouTubeAccounts) Current() (YouTubeAccount, bool) {
	if y.Anonymous() {
		return YouTubeAccount{}, false
	}
	if a, ok := y.Find(y.Chosen); ok {
		return a, true
	}
	if len(y.Accounts) > 0 {
		return y.Accounts[0], true
	}
	return YouTubeAccount{}, false
}
