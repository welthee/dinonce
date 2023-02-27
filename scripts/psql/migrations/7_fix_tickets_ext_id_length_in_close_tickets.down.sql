-- Active: 1677414216697@@127.0.0.1@5433@postgres
create or replace function close_ticket(
    _lineage_id uuid,
    _lineage_version bigint,
    _ticket_ext_id character varying(64)
) returns void
    language plpgsql
as
$$
declare
    _now                    timestamptz;
    _newversion             bigint;
    _selected_ticket_ext_id character varying(64);
begin
    _now := now();

    update tickets
    set lease_status='closed'
    where lineage_id = _lineage_id
      and ext_id = _ticket_ext_id
      and lease_status = 'leased'
    returning ext_id into _selected_ticket_ext_id;

    if _selected_ticket_ext_id is null then
        select ext_id
        into _selected_ticket_ext_id
        from tickets
        where lineage_id = _lineage_id
          and ext_id = _ticket_ext_id
          and lease_status = 'closed';

        if _selected_ticket_ext_id is null then
            raise exception 'no_such_ticket';
        end if;

        raise exception 'already_closed';
    end if;

    update lineages
    set leased_nonce_count = lineages.leased_nonce_count - 1,
        version            = version + 1
    where id = _lineage_id
      and version = _lineage_version
    returning version into _newversion;

    if _newversion is null then
        raise exception 'optimistic_lock';
    end if;
end;
$$;