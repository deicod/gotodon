-- name: GetCurrentTermsOfService :one
SELECT * FROM terms_of_services
WHERE published_at IS NOT NULL
    AND (effective_date IS NULL OR effective_date < now())
ORDER BY COALESCE(effective_date, published_at) DESC
LIMIT 1;

-- name: GetUpcomingTermsOfService :one
SELECT * FROM terms_of_services
WHERE published_at IS NOT NULL
    AND effective_date IS NOT NULL
    AND effective_date > now()
ORDER BY effective_date ASC
LIMIT 1;

-- name: GetTermsOfServiceByEffectiveDate :one
SELECT * FROM terms_of_services
WHERE published_at IS NOT NULL
    AND effective_date = $1;

-- name: GetTermsOfServiceSucceededBy :one
SELECT * FROM terms_of_services
WHERE published_at IS NOT NULL
    AND effective_date IS NOT NULL
    AND effective_date > $1
    AND id <> $2
ORDER BY effective_date ASC
LIMIT 1;
