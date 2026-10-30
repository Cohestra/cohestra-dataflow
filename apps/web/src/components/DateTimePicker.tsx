export interface DateTimePickerProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
}

export function DateTimePicker({ value, onChange, placeholder = 'Select date & time', className = '' }: DateTimePickerProps) {
  return (
    <input
      type="datetime-local"
      step="1"
      value={value}
      onChange={event => onChange(event.target.value)}
      aria-label={placeholder}
      className={`glass-input min-w-[188px] py-1.5 text-[12px] scheme-light dark:scheme-dark ${className}`}
    />
  );
}
