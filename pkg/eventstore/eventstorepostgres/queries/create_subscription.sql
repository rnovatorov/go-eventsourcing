INSERT INTO es_subscriptions (id, "position")
    VALUES (@subscription_id, 0)
ON CONFLICT (id)
    DO NOTHING;
