-- Manager statistics (SPEC §16). Requests are counted by creation time; visits, visitors
-- and revenue by departure date, for completed bookings only.

-- name: VillageRequestStats :one
SELECT count(*)::bigint AS total,
       count(*) FILTER (WHERE status = 'pending')::bigint AS pending,
       count(*) FILTER (WHERE status IN ('confirmed', 'completed')
                          OR (status = 'cancelled' AND decided_at IS NOT NULL))::bigint AS accepted,
       count(*) FILTER (WHERE status = 'rejected')::bigint AS rejected,
       count(*) FILTER (WHERE status = 'expired')::bigint AS expired,
       count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
       count(*) FILTER (WHERE decided_at IS NOT NULL)::bigint AS responded,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY greatest(extract(epoch FROM decided_at - created_at), 0))
                FILTER (WHERE decided_at IS NOT NULL), 0)::float8 AS median_response_seconds
FROM bookings
WHERE village_id = @village_id AND created_at >= @from_time AND created_at < @to_time;

-- name: VillageVisitStats :one
SELECT count(*)::bigint AS visits,
       coalesce(sum(party_size), 0)::bigint AS visitors,
       coalesce(sum(total_price_idr), 0)::bigint AS revenue
FROM bookings
WHERE village_id = @village_id AND status = 'completed' AND start_date >= @from_date AND start_date < @to_date;

-- name: VillageMonthlyVisitors :many
SELECT date_trunc('month', start_date)::date AS month,
       count(*)::bigint AS visits,
       sum(party_size)::bigint AS visitors,
       sum(total_price_idr)::bigint AS revenue
FROM bookings
WHERE village_id = @village_id AND status = 'completed' AND start_date >= @from_date AND start_date < @to_date
GROUP BY 1
ORDER BY 1;

-- name: VillagePackageStats :many
SELECT p.id, p.name,
       count(b.id) FILTER (WHERE b.created_at >= @from_time AND b.created_at < @to_time)::bigint AS requests,
       coalesce(sum(b.party_size) FILTER (WHERE b.status = 'completed' AND b.start_date >= @from_date AND b.start_date < @to_date), 0)::bigint AS visitors,
       coalesce(sum(b.total_price_idr) FILTER (WHERE b.status = 'completed' AND b.start_date >= @from_date AND b.start_date < @to_date), 0)::bigint AS revenue
FROM tour_packages p
LEFT JOIN bookings b ON b.package_id = p.id
WHERE p.village_id = @village_id
GROUP BY p.id, p.name
ORDER BY visitors DESC, requests DESC, p.name
LIMIT 10;
