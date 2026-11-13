ALTER TABLE users ADD COLUMN nickname_changed_at timestamptz;

CREATE TABLE nickname_requests (
 id text PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id),
 previous_nickname text NOT NULL,
 nickname text NOT NULL CHECK(char_length(nickname) BETWEEN 1 AND 30),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
 review_note text NOT NULL DEFAULT '',
 reviewed_by text REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz,
 CHECK((status='pending' AND reviewed_at IS NULL AND reviewed_by IS NULL) OR (status<>'pending' AND reviewed_at IS NOT NULL AND reviewed_by IS NOT NULL))
);
CREATE UNIQUE INDEX nickname_requests_one_pending ON nickname_requests(user_id) WHERE status='pending';
CREATE INDEX nickname_requests_review_queue ON nickname_requests(status,created_at DESC,id);
CREATE INDEX nickname_requests_member_history ON nickname_requests(user_id,created_at DESC,id);

CREATE TABLE avatar_library (
 id text PRIMARY KEY,
 name text NOT NULL CHECK(char_length(name) BETWEEN 1 AND 30),
 media_id text UNIQUE NOT NULL REFERENCES media_files(id),
 sort_order integer NOT NULL DEFAULT 0 CHECK(sort_order BETWEEN 0 AND 1000000),
 enabled boolean NOT NULL DEFAULT true,
 version integer NOT NULL DEFAULT 1,
 created_by text NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX avatar_library_display ON avatar_library(sort_order,id) WHERE enabled;
