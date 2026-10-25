package publicgame

type Game struct {
	ID          string  `json:"id" bson:"game_id"`
	Name        string  `json:"name" bson:"name"`
	Slug        string  `json:"slug" bson:"slug"`
	Description *string `json:"description,omitempty" bson:"description"`
	ImageURL    string  `json:"imageUrl" bson:"image_url"`
	Platform    *string `json:"platform,omitempty" bson:"platform"`
	IsPopular   *bool   `json:"isPopular,omitempty" bson:"is_popular,omitempty"`
	SortOrder   int64   `json:"sortOrder" bson:"sort_order"`
}

type Filter struct {
	Search   string
	Platform string
	Sort     string
	Page     int64
	Limit    int64
	Popular  bool
}

type Page struct {
	Items      []Game
	Page       int64
	Limit      int64
	Total      int64
	TotalPages int64
}

type Pagination struct {
	Page       int64 `json:"page"`
	Limit      int64 `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"totalPages"`
}

type listResponse struct {
	Code       string     `json:"code"`
	Message    string     `json:"message"`
	Data       []Game     `json:"data"`
	Pagination Pagination `json:"pagination"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}
