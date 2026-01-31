-- Adds materialized views for follow recommendations and aligns column types.

-- +goose Up

CREATE MATERIALIZED VIEW account_summaries AS
    SELECT accounts.id AS account_id,
           mode() WITHIN GROUP (ORDER BY t0.language) AS language,
           mode() WITHIN GROUP (ORDER BY t0.sensitive) AS sensitive
      FROM (accounts
            CROSS JOIN LATERAL (SELECT statuses.account_id,
                                       statuses.language,
                                       statuses.sensitive
                                  FROM statuses
                                 WHERE ((statuses.account_id = accounts.id) AND (statuses.deleted_at IS NULL) AND (statuses.reblog_of_id IS NULL))
                                 ORDER BY statuses.id DESC
                                 LIMIT 20) t0)
     WHERE ((accounts.suspended_at IS NULL) AND (accounts.silenced_at IS NULL) AND (accounts.moved_to_account_id IS NULL) AND (accounts.discoverable = true) AND (accounts.locked = false))
     GROUP BY accounts.id;

CREATE INDEX idx_on_account_id_language_sensitive_250461e1eb ON account_summaries (account_id, language, sensitive);
CREATE UNIQUE INDEX index_account_summaries_on_account_id ON account_summaries (account_id);

CREATE MATERIALIZED VIEW global_follow_recommendations AS
    SELECT account_id,
           sum(rank) AS rank,
           array_agg(reason) AS reason
      FROM ( SELECT account_summaries.account_id,
                    ((count(follows.id))::numeric / (1.0 + (count(follows.id))::numeric)) AS rank,
                    'most_followed'::text AS reason
               FROM ((follows
                     JOIN account_summaries ON ((account_summaries.account_id = follows.target_account_id)))
                     JOIN users ON ((users.account_id = follows.account_id)))
              WHERE ((users.current_sign_in_at >= (now() - 'P30D'::interval)) AND (account_summaries.sensitive = false) AND (NOT (EXISTS ( SELECT 1
                       FROM follow_recommendation_suppressions
                      WHERE (follow_recommendation_suppressions.account_id = follows.target_account_id)))))
              GROUP BY account_summaries.account_id
             HAVING (count(follows.id) >= 5)
            UNION ALL
             SELECT account_summaries.account_id,
                    (sum((status_stats.reblogs_count + status_stats.favourites_count)) / (1.0 + sum((status_stats.reblogs_count + status_stats.favourites_count)))) AS rank,
                    'most_interactions'::text AS reason
               FROM ((status_stats
                     JOIN statuses ON ((statuses.id = status_stats.status_id)))
                     JOIN account_summaries ON ((account_summaries.account_id = statuses.account_id)))
              WHERE ((statuses.id >= (((date_part('epoch'::text, (now() - 'P30D'::interval)) * (1000)::double precision))::bigint << 16)) AND (account_summaries.sensitive = false) AND (NOT (EXISTS ( SELECT 1
                       FROM follow_recommendation_suppressions
                      WHERE (follow_recommendation_suppressions.account_id = statuses.account_id)))))
              GROUP BY account_summaries.account_id
             HAVING (sum((status_stats.reblogs_count + status_stats.favourites_count)) >= (5)::numeric)) t0
     GROUP BY account_id
     ORDER BY (sum(rank)) DESC;

CREATE UNIQUE INDEX index_global_follow_recommendations_on_account_id ON global_follow_recommendations (account_id);

CREATE MATERIALIZED VIEW instances AS
    WITH domain_counts(domain, accounts_count) AS (
         SELECT accounts.domain,
                count(*) AS accounts_count
           FROM accounts
          WHERE (accounts.domain IS NOT NULL)
          GROUP BY accounts.domain
        )
    SELECT domain_counts.domain,
           domain_counts.accounts_count
      FROM domain_counts
    UNION
    SELECT domain_blocks.domain,
           COALESCE(domain_counts.accounts_count, (0)::bigint) AS accounts_count
      FROM (domain_blocks
            LEFT JOIN domain_counts ON (((domain_counts.domain)::text = (domain_blocks.domain)::text)))
    UNION
    SELECT domain_allows.domain,
           COALESCE(domain_counts.accounts_count, (0)::bigint) AS accounts_count
      FROM (domain_allows
            LEFT JOIN domain_counts ON (((domain_counts.domain)::text = (domain_allows.domain)::text)));

CREATE INDEX index_instances_on_reverse_domain ON instances (reverse(('.'::text || (domain)::text)), domain);
CREATE UNIQUE INDEX index_instances_on_domain ON instances (domain);

CREATE VIEW user_ips AS
    SELECT user_id,
           ip,
           max(used_at) AS used_at
      FROM ( SELECT users.id AS user_id,
                    users.sign_up_ip AS ip,
                    users.created_at AS used_at
               FROM users
              WHERE (users.sign_up_ip IS NOT NULL)
            UNION ALL
             SELECT session_activations.user_id,
                    session_activations.ip,
                    session_activations.updated_at
               FROM session_activations
            UNION ALL
             SELECT login_activities.user_id,
                    login_activities.ip,
                    login_activities.created_at
               FROM login_activities
              WHERE (login_activities.success = true)) t0
     GROUP BY user_id, ip;

ALTER TABLE preview_cards ALTER COLUMN description TYPE varchar(255);
ALTER TABLE webauthn_credentials ALTER COLUMN public_key TYPE varchar(255);

-- +goose Down

ALTER TABLE webauthn_credentials ALTER COLUMN public_key TYPE text;
ALTER TABLE preview_cards ALTER COLUMN description TYPE text;

DROP VIEW IF EXISTS user_ips;

DROP INDEX IF EXISTS index_instances_on_domain;
DROP INDEX IF EXISTS index_instances_on_reverse_domain;
DROP MATERIALIZED VIEW IF EXISTS instances;

DROP INDEX IF EXISTS index_global_follow_recommendations_on_account_id;
DROP MATERIALIZED VIEW IF EXISTS global_follow_recommendations;

DROP INDEX IF EXISTS index_account_summaries_on_account_id;
DROP INDEX IF EXISTS idx_on_account_id_language_sensitive_250461e1eb;
DROP MATERIALIZED VIEW IF EXISTS account_summaries;
