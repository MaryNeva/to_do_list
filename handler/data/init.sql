CREATE USER "user1" WITH PASSWORD 'pass123';
CREATE DATABASE to_do OWNER "user1";


DO $$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'status') THEN
            CREATE TYPE status AS ENUM ('created', 'in_progress', 'completed');
        END IF;
    END$$;



-- Table tasks
CREATE TABLE IF NOT EXISTS public.tasks (
                                            id bigserial PRIMARY KEY,
                                            title TEXT NOT NULL,
                                            description TEXT NOT NULL,
                                            status status NOT NULL DEFAULT 'created',
                                            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);


-- Table users
CREATE TABLE IF NOT EXISTS public.users (
                                            id bigserial PRIMARY KEY,
                                            username TEXT NOT NULL,
                                            password TEXT NOT NULL,
                                            email TEXT NOT NULL UNIQUE,
                                            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);




-- Table user_tasks
CREATE TABLE IF NOT EXISTS public.user_tasks (
                                                 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                                 task_id bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
                                                 PRIMARY KEY (user_id, task_id)
);



-- Auto update updated_at function
CREATE OR REPLACE FUNCTION update_timestamp()
    RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER set_timestamp_tasks
    BEFORE UPDATE ON public.tasks
    FOR EACH ROW
EXECUTE FUNCTION update_timestamp();

CREATE TRIGGER set_timestamp_users
    BEFORE UPDATE ON public.users
    FOR EACH ROW
EXECUTE FUNCTION update_timestamp();



CREATE OR REPLACE FUNCTION unset_default_tasks_if_deleted()
    RETURNS TRIGGER AS $$
BEGIN
    -- логика
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;



