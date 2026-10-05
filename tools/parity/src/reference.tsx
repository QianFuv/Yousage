/** Browser-only entry for the pinned component fixture workbench. */
import { createRoot } from 'react-dom/client';
import { Reference } from './workbench';
createRoot(document.getElementById('root')!).render(<Reference />);
