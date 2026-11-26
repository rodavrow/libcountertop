package schema

type Mealie struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func DefaultMealie() Mealie { return Mealie{} }
