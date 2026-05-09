import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '@/components/Spinner';

// /errors got renamed to /anomalies — exceptions are one of
// several anomaly signals now (log-pattern spikes, new errors,
// metric deviations). Keep the old route as a silent redirect so
// shared links and bookmarks don't 404.
export default function ErrorsRedirectPage() {
  const navigate = useNavigate();
  useEffect(() => { navigate('/anomalies'); }, [navigate]);
  return <Spinner />;
}
