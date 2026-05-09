import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '@/components/Spinner';

// /status is now folded into /admin/stats — keep the route as a
// silent redirect so old bookmarks / shared links land the user
// on the merged page instead of 404'ing.
export default function StatusRedirectPage() {
  const navigate = useNavigate();
  useEffect(() => {
    navigate('/admin/stats');
  }, [navigate]);
  return <Spinner />;
}
