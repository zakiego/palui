-- MCP access: Palui serves /mcp to the owner's own AI agents (claude.ai,
-- Claude Code, mcporter). Personal tokens and OAuth connections are both rows
-- in mcp_grants; oauth_clients and oauth_requests back the OAuth 2.1 flow
-- (dynamic client registration, PKCE). New tables only. See internal/mcpaccess.
CREATE TABLE `oauth_clients` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`client_uri` text,
	`redirect_uris` text NOT NULL,
	`auth_method` text NOT NULL,
	`secret_hash` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `oauth_requests` (
	`id` text PRIMARY KEY NOT NULL,
	`client_id` text NOT NULL,
	`redirect_uri` text NOT NULL,
	`state` text,
	`scope` text,
	`code_challenge` text NOT NULL,
	`user_id` text,
	`code_hash` text,
	`created_at` integer NOT NULL,
	`expires_at` integer NOT NULL,
	FOREIGN KEY (`client_id`) REFERENCES `oauth_clients`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `oauth_requests_code_hash_unique` ON `oauth_requests` (`code_hash`);
--> statement-breakpoint
CREATE INDEX `oauth_requests_client_idx` ON `oauth_requests` (`client_id`);
--> statement-breakpoint
CREATE TABLE `mcp_grants` (
	`id` text PRIMARY KEY NOT NULL,
	`kind` text NOT NULL,
	`name` text NOT NULL,
	`user_id` text NOT NULL,
	`client_id` text,
	`scope` text,
	`token_hint` text,
	`access_hash` text NOT NULL,
	`access_expires_at` integer,
	`refresh_hash` text,
	`refresh_expires_at` integer,
	`prev_refresh_hash` text,
	`rotated_at` integer,
	`created_at` integer NOT NULL,
	`last_used_at` integer,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`client_id`) REFERENCES `oauth_clients`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `mcp_grants_access_hash_unique` ON `mcp_grants` (`access_hash`);
--> statement-breakpoint
CREATE UNIQUE INDEX `mcp_grants_refresh_hash_unique` ON `mcp_grants` (`refresh_hash`);
--> statement-breakpoint
CREATE INDEX `mcp_grants_prev_refresh_hash_idx` ON `mcp_grants` (`prev_refresh_hash`);
--> statement-breakpoint
CREATE INDEX `mcp_grants_user_idx` ON `mcp_grants` (`user_id`);
--> statement-breakpoint
CREATE INDEX `mcp_grants_client_idx` ON `mcp_grants` (`client_id`);
