-- Add admin/user roles so globally shared reference data (account types) can be
-- restricted to admins. New users default to the 'user' role; promotion to
-- 'admin' happens via the ADMIN_EMAILS setting at registration/startup.
ALTER TABLE users ADD COLUMN role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'));