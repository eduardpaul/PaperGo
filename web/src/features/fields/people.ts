import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { usersQuery } from '@/api/queries';

/** Display names of organization users by subject; subjects stay as they are when the caller cannot read users. */
export function usePeopleNames() {
  const users = useQuery(usersQuery);
  return useMemo(
    () => new Map((users.data ?? []).map((user) => [user.subject, user.display_name || user.subject])),
    [users.data],
  );
}
