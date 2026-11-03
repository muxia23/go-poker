-- Accounts are who may use the site. Times are unix ms; secrets are stored hashed.

create table accounts (
  id            text primary key,          -- uuid, never changes
  username      text not null,             -- as they typed it
  username_key  text not null unique,      -- normalized, what uniqueness is checked on
  password_hash text not null,
  role          text not null,             -- root, admin or user
  disabled      integer not null default 0,
  must_change   integer not null default 0, -- has to set a new password first
  created_at    bigint not null,
  last_login    bigint not null default 0
);
create unique index accounts_one_root on accounts (role) where role = 'root';

create table invites (
  id         text primary key,
  code_hash  text not null unique,
  created_by text not null,
  created_at bigint not null,
  expires_at bigint not null,
  revoked_at bigint not null default 0,
  used_by    text not null default '',
  used_at    bigint not null default 0
);

create table sessions (
  token_hash text primary key,
  account_id text not null,
  expires_at bigint not null
);
create index sessions_account on sessions (account_id);
