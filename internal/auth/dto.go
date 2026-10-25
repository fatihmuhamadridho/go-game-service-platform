package auth

type LoginRequestDTO struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type LoginResponseDTO struct {
	AdminProfile
	CSRFToken     string `json:"csrfToken"`
	AccessExpires int    `json:"accessExpiresIn"`
}

type RefreshResponseDTO struct {
	CSRFToken     string `json:"csrfToken"`
	AccessExpires int    `json:"accessExpiresIn"`
}
