// Minimal test-only surface: React DOM is installed, its optional types are not.
declare module 'react-dom/client' {
  export function createRoot(container: Element): {
    render(children: import('react').ReactNode): void;
    unmount(): void;
  };
}