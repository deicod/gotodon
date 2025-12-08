// Package testing provides Mastodon API test vectors.
package testing

// AccountTestVectors returns test vectors for account endpoints
func AccountTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "verify_credentials requires auth",
			Method:         "GET",
			Path:           "/api/v1/accounts/verify_credentials",
			ExpectedStatus: 401,
		},
		{
			Name:           "verify_credentials with auth",
			Method:         "GET",
			Path:           "/api/v1/accounts/verify_credentials",
			AccountID:      12345,
			ExpectedStatus: 200,
			ExpectedFields: []string{"id", "username", "acct"},
		},
		{
			Name:           "get account by id",
			Method:         "GET",
			Path:           "/api/v1/accounts/12345",
			ExpectedStatus: 200,
			ExpectedFields: []string{"id", "username"},
		},
		{
			Name:           "get nonexistent account",
			Method:         "GET",
			Path:           "/api/v1/accounts/99999999",
			ExpectedStatus: 404,
		},
	}
}

// StatusTestVectors returns test vectors for status endpoints
func StatusTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "create status requires auth",
			Method:         "POST",
			Path:           "/api/v1/statuses",
			Body:           `{"status":"Hello world"}`,
			ExpectedStatus: 401,
		},
		{
			Name:           "create status with auth",
			Method:         "POST",
			Path:           "/api/v1/statuses",
			Body:           `{"status":"Hello world"}`,
			AccountID:      12345,
			ExpectedStatus: 200,
			ExpectedFields: []string{"id", "content", "created_at"},
		},
		{
			Name:           "create status without content",
			Method:         "POST",
			Path:           "/api/v1/statuses",
			Body:           `{}`,
			AccountID:      12345,
			ExpectedStatus: 422,
		},
	}
}

// TimelineTestVectors returns test vectors for timeline endpoints
func TimelineTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "public timeline",
			Method:         "GET",
			Path:           "/api/v1/timelines/public",
			ExpectedStatus: 200,
		},
		{
			Name:           "home timeline requires auth",
			Method:         "GET",
			Path:           "/api/v1/timelines/home",
			ExpectedStatus: 401,
		},
		{
			Name:           "home timeline with auth",
			Method:         "GET",
			Path:           "/api/v1/timelines/home",
			AccountID:      12345,
			ExpectedStatus: 200,
		},
		{
			Name:           "hashtag timeline",
			Method:         "GET",
			Path:           "/api/v1/timelines/tag/golang",
			ExpectedStatus: 200,
		},
	}
}

// NotificationTestVectors returns test vectors for notification endpoints
func NotificationTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "get notifications requires auth",
			Method:         "GET",
			Path:           "/api/v1/notifications",
			ExpectedStatus: 401,
		},
		{
			Name:           "get notifications with auth",
			Method:         "GET",
			Path:           "/api/v1/notifications",
			AccountID:      12345,
			ExpectedStatus: 200,
		},
		{
			Name:           "clear notifications",
			Method:         "POST",
			Path:           "/api/v1/notifications/clear",
			AccountID:      12345,
			ExpectedStatus: 200,
		},
	}
}

// ListTestVectors returns test vectors for list endpoints
func ListTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "get lists requires auth",
			Method:         "GET",
			Path:           "/api/v1/lists",
			ExpectedStatus: 401,
		},
		{
			Name:           "get lists with auth",
			Method:         "GET",
			Path:           "/api/v1/lists",
			AccountID:      12345,
			ExpectedStatus: 200,
		},
		{
			Name:           "create list",
			Method:         "POST",
			Path:           "/api/v1/lists",
			Body:           `{"title":"My List"}`,
			AccountID:      12345,
			ExpectedStatus: 200,
			ExpectedFields: []string{"id", "title", "replies_policy"},
		},
		{
			Name:           "create list without title",
			Method:         "POST",
			Path:           "/api/v1/lists",
			Body:           `{}`,
			AccountID:      12345,
			ExpectedStatus: 422,
		},
	}
}

// PollTestVectors returns test vectors for poll endpoints
func PollTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "get poll",
			Method:         "GET",
			Path:           "/api/v1/polls/12345",
			ExpectedStatus: 404, // No poll with this ID
		},
		{
			Name:           "vote on poll requires auth",
			Method:         "POST",
			Path:           "/api/v1/polls/12345/votes",
			Body:           `{"choices":[0]}`,
			ExpectedStatus: 401,
		},
	}
}

// AdminTestVectors returns test vectors for admin endpoints
func AdminTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "admin accounts requires admin role",
			Method:         "GET",
			Path:           "/api/v1/admin/accounts",
			AccountID:      12345,
			Role:           "user",
			ExpectedStatus: 403,
		},
		{
			Name:           "admin accounts with admin role",
			Method:         "GET",
			Path:           "/api/v1/admin/accounts",
			AccountID:      12345,
			Role:           "admin",
			ExpectedStatus: 200,
		},
		{
			Name:           "admin reports",
			Method:         "GET",
			Path:           "/api/v1/admin/reports",
			AccountID:      12345,
			Role:           "admin",
			ExpectedStatus: 200,
		},
		{
			Name:           "admin domain blocks",
			Method:         "GET",
			Path:           "/api/v1/admin/domain_blocks",
			AccountID:      12345,
			Role:           "admin",
			ExpectedStatus: 200,
		},
	}
}

// FilterTestVectors returns test vectors for filter endpoints
func FilterTestVectors() []APITestCase {
	return []APITestCase{
		{
			Name:           "get filters requires auth",
			Method:         "GET",
			Path:           "/api/v2/filters",
			ExpectedStatus: 401,
		},
		{
			Name:           "get filters with auth",
			Method:         "GET",
			Path:           "/api/v2/filters",
			AccountID:      12345,
			ExpectedStatus: 200,
		},
		{
			Name:           "create filter",
			Method:         "POST",
			Path:           "/api/v2/filters",
			Body:           `{"title":"Test","context":["home"],"filter_action":"warn"}`,
			AccountID:      12345,
			ExpectedStatus: 200,
			ExpectedFields: []string{"id", "title", "context", "filter_action"},
		},
	}
}

// AllTestVectors returns all test vectors
func AllTestVectors() []APITestCase {
	var all []APITestCase
	all = append(all, AccountTestVectors()...)
	all = append(all, StatusTestVectors()...)
	all = append(all, TimelineTestVectors()...)
	all = append(all, NotificationTestVectors()...)
	all = append(all, ListTestVectors()...)
	all = append(all, PollTestVectors()...)
	all = append(all, AdminTestVectors()...)
	all = append(all, FilterTestVectors()...)
	return all
}
