-- name: CreateAdminActionLog :exec
INSERT INTO admin_action_logs (
    account_id,
    action,
    target_type,
    target_id,
    human_identifier,
    route_param,
    permalink
) VALUES (
    $1,
    $2,
    $3,
    sqlc.narg('target_id'),
    sqlc.narg('human_identifier'),
    sqlc.narg('route_param'),
    sqlc.narg('permalink')
);
