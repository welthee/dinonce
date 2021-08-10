drop table if exists tickets;
drop table if exists released_tickets;
drop table if exists lineages;

drop type if exists ticket_lease_status;

drop sequence if exists lockz_rvn;
drop table if exists lockz;

drop function if exists create_ticket;
drop function if exists release_ticket;
drop function if exists close_ticket;
