CREATE USER "user1" WITH PASSWORD 'password';
CREATE DATABASE to_do OWNER "user1";


DO $$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'status') THEN
            CREATE TYPE status AS ENUM ('created', 'inprogress', 'completed');
        END IF;
    END$$;



-- Table tasks
CREATE TABLE IF NOT EXISTS public.tasks (
                                            id bigserial PRIMARY KEY,
                                            title TEXT NOT NULL,
                                            description TEXT NOT NULL,
                                            completed bool default false,
                                            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                            status status NOT NULL DEFAULT 'created'
);

-- Table users
CREATE TABLE IF NOT EXISTS public.users (
                                            id bigserial UNIQUE PRIMARY KEY,
                                            secret TEXT NOT NULL UNIQUE,
                                            username TEXT NOT NULL,
                                            password TEXT NOT NULL,
                                            email TEXT NOT NULL,
                                            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP

);



-- Table user_tasks
CREATE TABLE IF NOT EXISTS public.user_tasks (
                                            user_id smallserial REFERENCES users(id) ON DELETE CASCADE,
                                            task_id smallserial REFERENCES tasks(id) ON DELETE CASCADE,
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

CREATE TRIGGER set_timestamp
    BEFORE UPDATE ON public.tasks
    FOR EACH ROW
EXECUTE FUNCTION update_timestamp();



-- Create a trigger on the user_tasks table
CREATE TRIGGER trg_unset_default_tasks
    AFTER DELETE ON user_tasks
    FOR EACH ROW
EXECUTE FUNCTION unset_default_tasks_if_deleted();


