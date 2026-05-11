import React from 'react';
import { UseFormRegister, FieldArrayWithId, FieldErrors } from 'react-hook-form';
import { RecipientField } from './RecipientField';

// CSS for dark theme scrollbar (Receive design tokens)
const scrollbarStyles = `
  .recipient-scroll-container::-webkit-scrollbar {
    width: 8px;
  }
  .recipient-scroll-container::-webkit-scrollbar-track {
    background: #252525;
    border-radius: 4px;
  }
  .recipient-scroll-container::-webkit-scrollbar-thumb {
    background: #444;
    border-radius: 4px;
  }
  .recipient-scroll-container::-webkit-scrollbar-thumb:hover {
    background: #555;
  }
`;

interface RecipientData {
  address: string;
  amount: string;
  label?: string;
}

export interface SendRecipientsProps {
  fields: FieldArrayWithId<any, 'recipients', 'id'>[];
  register: UseFormRegister<any>;
  watchedRecipients: RecipientData[];
  onRemove: (index: number) => void;
  onUseMaximum: (index: number) => void;
  onAddressBookPick?: (index: number) => void;
  onSaveToAddressBook?: (address: string, label: string) => void;
  errors?: FieldErrors<{ recipients: RecipientData[] }>;
}

export const SendRecipients: React.FC<SendRecipientsProps> = ({
  fields,
  register,
  watchedRecipients,
  onRemove,
  onUseMaximum,
  onAddressBookPick,
  onSaveToAddressBook,
  errors,
}) => {
  return (
    <>
      <style>{scrollbarStyles}</style>
      <div
        className="recipient-scroll-container"
        style={{
          maxHeight: '280px',
          overflowY: 'auto',
          overflowX: 'hidden',
          scrollbarWidth: 'thin',
          scrollbarColor: '#555 #252525',
          display: 'flex',
          flexDirection: 'column',
          gap: '12px',
        }}
      >
        {fields.map((field, index) => (
          <RecipientField
            key={field.id}
            index={index}
            register={register}
            address={watchedRecipients?.[index]?.address || ''}
            label={watchedRecipients?.[index]?.label || ''}
            showRemoveButton={fields.length > 1}
            onRemove={() => onRemove(index)}
            onUseMaximum={() => onUseMaximum(index)}
            onAddressBookPick={onAddressBookPick ? () => onAddressBookPick(index) : undefined}
            onSaveToAddressBook={onSaveToAddressBook}
            errors={errors?.recipients?.[index]}
          />
        ))}
      </div>
    </>
  );
};
